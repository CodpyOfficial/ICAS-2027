package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"icas/internal/content"
)

// viewData is what every template receives.
type viewData struct {
	Site         *content.Site
	Page         *Page
	Nav          []navView
	Sidebar      *navView
	Crumbs       []crumb
	CSRF         string
	Form         *formState
	Now          time.Time
	CanonicalURL string
	BaseURL      string
	Data         any // page specific extras
}

type crumb struct {
	Label string
	Path  string
}

type navView struct {
	Label    string
	Path     string
	Active   bool
	External bool
	Note     string
	Divider  bool
	Children []navView
}

// templateSet maps a page template name to a fully parsed template tree
// (layout + partials + that page).
type templateSet map[string]*template.Template

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"asset": func(p string) string {
			return "/static/" + strings.TrimPrefix(p, "/") + "?v=" + s.assetVersion
		},
		"icon": func(name string, class ...string) template.HTML {
			c := "icon"
			if len(class) > 0 {
				c += " " + strings.Join(class, " ")
			}
			return template.HTML(fmt.Sprintf(
				`<svg class="%s" aria-hidden="true" focusable="false"><use href="/static/img/icons.svg?v=%s#%s"></use></svg>`,
				template.HTMLEscapeString(c), s.assetVersion, template.HTMLEscapeString(name)))
		},
		// tba renders v, or a TBA badge when v is blank.
		"tba": func(v string) template.HTML {
			if strings.TrimSpace(v) == "" {
				return template.HTML(`<span class="badge badge-tba">TBA</span>`)
			}
			return template.HTML(template.HTMLEscapeString(v))
		},
		"orText": func(v, fallback string) string {
			if strings.TrimSpace(v) == "" {
				return fallback
			}
			return v
		},
		"blank": func(v string) bool { return strings.TrimSpace(v) == "" },
		"past":  func(d content.KeyDate) bool { return d.Past(s.now()) },
		"isExternal": func(u string) bool {
			return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
		},
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i + 1
			}
			return out
		},
		"add":   func(a, b int) int { return a + b },
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"join":  strings.Join,
		"pad2":  func(n int) string { return fmt.Sprintf("%02d", n) },
		"first": func(n int, items []content.NewsItem) []content.NewsItem {
			if len(items) <= n {
				return items
			}
			return items[:n]
		},
		"wordLimit": func(key string) int { return wordLimits[key] },
		"field": func(f *formState, name string) string {
			if f == nil {
				return ""
			}
			return f.Values[name]
		},
		"fieldErr": func(f *formState, name string) string {
			if f == nil {
				return ""
			}
			return f.Errors[name]
		},
		"checked": func(f *formState, name, value string) bool {
			if f == nil {
				return false
			}
			for _, v := range f.Multi[name] {
				if v == value {
					return true
				}
			}
			return f.Values[name] == value
		},
		"track": func(key string) content.Track {
			t, _ := s.site().Track(key)
			return t
		},
		"applies": func(r content.Requirement, key string) bool {
			return r.Applies == "" || r.Applies == "all" || r.Applies == key
		},
		"socialLabel": func(network string) string {
			switch network {
			case "linkedin":
				return "LinkedIn"
			case "x-logo":
				return "X (Twitter)"
			case "facebook":
				return "Facebook"
			case "youtube":
				return "YouTube"
			case "github":
				return "GitHub"
			}
			return network
		},
		"benefit": func(v string) template.HTML {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "yes", "✓":
				return template.HTML(`<span class="tick" aria-label="Included">&#10003;</span>`)
			case "no", "", "-", "–":
				return template.HTML(`<span class="dash" aria-label="Not included">&ndash;</span>`)
			}
			return template.HTML(template.HTMLEscapeString(v))
		},
		"year": func() int { return s.now().Year() },
		// dict builds a map so partial templates can take named arguments.
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
		"slug":   content.Slug,
		"pretty": prettyJSON,
		"event": func(slug string) content.Event {
			e, _ := s.site().Event(slug)
			return e
		},
		"eventPath": eventPath,
		"call": func(key string) content.Call {
			for _, c := range s.site().Calls {
				if c.Key == key {
					return c
				}
			}
			return content.Call{Key: key, Status: "soon"}
		},
		"skillLevels":   func() []string { return skillLevels },
		"workshopSizes": func() []string { return workshopSizes },
		"taCounts":      func() []string { return taCounts },
		"trackName":     trackName,
		"workFields":    func() []string { return workFields },
		"interests":     func() []string { return interestOptions },
		"contactTopics": func() []string { return contactTopics },
		"hasSponsors": func(tiers []content.SponsorTier) bool {
			for _, t := range tiers {
				if len(t.Sponsors) > 0 {
					return true
				}
			}
			return false
		},
		"hasPeople": func(groups []content.PeopleGroup) bool {
			for _, g := range groups {
				if len(g.People) > 0 {
					return true
				}
			}
			return false
		},
		"named": func(ss []content.Speaker) bool {
			for _, sp := range ss {
				if sp.Name != "" {
					return true
				}
			}
			return false
		},
		// newGroup reports whether benefit row i starts a new group.
		"newGroup": func(rows []content.BenefitRow, i int) bool {
			return i == 0 || rows[i].Group != rows[i-1].Group
		},
		"callBadge": func(status string) template.HTML {
			switch status {
			case "open":
				return template.HTML(`<span class="badge badge-open">Open</span>`)
			case "closed":
				return template.HTML(`<span class="badge badge-closed">Closed</span>`)
			}
			return template.HTML(`<span class="badge badge-soon">Opens soon</span>`)
		},
		"mailtoSubject": func(email, subject string) string {
			return "mailto:" + email + "?subject=" + strings.ReplaceAll(url.QueryEscape(subject), "+", "%20")
		},
	}
}

// loadTemplates parses the layout and partials once and clones them for
// every page so that each page can define its own "content" block.
func (s *Server) loadTemplates(fsys fs.FS) (templateSet, error) {
	base, err := template.New("").Funcs(s.funcs()).ParseFS(fsys, "templates/layouts/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	pages, err := fs.Glob(fsys, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	set := make(templateSet, len(pages))
	for _, p := range pages {
		t, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := t.ParseFS(fsys, p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		set[strings.TrimSuffix(path.Base(p), ".html")] = t
	}
	return set, nil
}

// assetHash fingerprints every static file so URLs change whenever any
// asset changes, which lets static responses be cached for a year.
func assetHash(fsys fs.FS) (string, error) {
	var names []string
	err := fs.WalkDir(fsys, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return "", err
		}
		h.Write([]byte(n))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:10], nil
}

// render executes the page template into a buffer first, so a template
// error becomes a clean 500 instead of a half-written page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page *Page, form *formState, extra any) {
	set, err := s.templates()
	if err != nil {
		s.serverError(w, err)
		return
	}
	t, ok := set[page.Template]
	if !ok {
		s.serverError(w, fmt.Errorf("no template %q for %s", page.Template, page.Path))
		return
	}
	base := s.baseURL(r)
	data := viewData{
		Site:         s.site(),
		Page:         page,
		Nav:          s.navFor(page),
		Sidebar:      s.sidebarMenu(page),
		Crumbs:       s.crumbsFor(page),
		CSRF:         csrfToken(w, r),
		Form:         form,
		Now:          s.now(),
		CanonicalURL: base + page.Path,
		BaseURL:      base,
		Data:         extra,
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		s.serverError(w, fmt.Errorf("render %s: %w", page.Template, err))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	if page.Private {
		h.Set("Cache-Control", "no-store")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if page.NoIndex {
		h.Set("X-Robots-Tag", "noindex, nofollow")
	}
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	s.logger.Printf("error: %v", err)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func (s *Server) baseURL(r *http.Request) string {
	if b := strings.TrimRight(s.cfg.BaseURL, "/"); b != "" {
		return b
	}
	if b := strings.TrimRight(s.site().Conference.BaseURL, "/"); b != "" {
		return b
	}
	scheme := "http"
	if r.TLS != nil || (s.cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
