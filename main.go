// Command icas serves the ICAS (International Conference on Autonomous
// System) website.
//
// Templates, static assets and content/site.json are embedded in the
// binary, so a single executable is all that has to be deployed. During
// editing, run with -dev to read them from disk on every request instead.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"icas/internal/content"
	"icas/internal/server"
)

//go:embed web/templates web/static content/site.json
var embedded embed.FS

func main() {
	defaultAddr := env("ICAS_ADDR", ":8090")
	if p := os.Getenv("PORT"); p != "" && os.Getenv("ICAS_ADDR") == "" {
		defaultAddr = ":" + p
	}
	var (
		addr       = flag.String("addr", defaultAddr, "listen address (env ICAS_ADDR or PORT)")
		dataDir    = flag.String("data", env("ICAS_DATA_DIR", "data"), "directory for submissions, uploaded papers, contact messages and subscribers (env ICAS_DATA_DIR)")
		baseURL    = flag.String("base-url", env("ICAS_BASE_URL", ""), "public origin used in canonical links and the sitemap, e.g. https://icas.example.org (env ICAS_BASE_URL)")
		dev        = flag.Bool("dev", os.Getenv("ICAS_DEV") == "1", "read templates, static files and content/site.json from disk on every request (env ICAS_DEV=1)")
		trustProxy = flag.Bool("trust-proxy", os.Getenv("ICAS_TRUST_PROXY") == "1", "trust X-Forwarded-For/-Proto from a reverse proxy (env ICAS_TRUST_PROXY=1)")
	)
	flag.Parse()

	logger := log.New(os.Stderr, "icas ", log.LstdFlags)

	var webFS fs.FS
	var loadSite func() (*content.Site, error)
	if *dev {
		webFS = os.DirFS("web")
		loadSite = func() (*content.Site, error) { return content.LoadFile("content/site.json") }
		logger.Print("dev mode: templates, assets and content are read from disk on each request")
	} else {
		sub, err := fs.Sub(embedded, "web")
		if err != nil {
			logger.Fatal(err)
		}
		webFS = sub
		loadSite = func() (*content.Site, error) { return content.LoadFS(embedded, "content/site.json") }
	}

	srv, err := server.New(server.Config{
		DataDir:       *dataDir,
		BaseURL:       *baseURL,
		Dev:           *dev,
		TrustProxy:    *trustProxy,
		AdminUser:     os.Getenv("ICAS_ADMIN_USER"),
		AdminPassword: os.Getenv("ICAS_ADMIN_PASSWORD"),
		Logger:        logger,
	}, webFS, loadSite)
	if err != nil {
		logger.Fatal(err)
	}

	// The timeouts suit pages; the paper upload and the paper downloads
	// extend them for their own request.
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          logger,
	}

	go func() {
		logger.Printf("listening on %s (data directory: %s)", *addr, *dataDir)
		if os.Getenv("ICAS_ADMIN_PASSWORD") == "" {
			logger.Print("admin pages disabled: set ICAS_ADMIN_PASSWORD to enable /admin")
		}
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Print("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		logger.Printf("shutdown: %v", err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
