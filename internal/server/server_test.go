package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"icas/internal/content"
	"icas/internal/store"
)

var fixedNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func newTestServer(t *testing.T, mutate func(*Config)) *Server {
	t.Helper()
	root := filepath.Join("..", "..")
	cfg := Config{
		DataDir:       t.TempDir(),
		Logger:        log.New(io.Discard, "", 0),
		AdminUser:     "admin",
		AdminPassword: "s3cret",
		Now:           func() time.Time { return fixedNow },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	load := func() (*content.Site, error) { return content.LoadFile(filepath.Join(root, "content", "site.json")) }
	s, err := New(cfg, os.DirFS(filepath.Join(root, "web")), load)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, s *Server, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

var tokenRe = regexp.MustCompile(`name="csrf_token" value="([0-9a-f]{64})"`)

// session fetches a form page and returns the CSRF cookie and token.
func session(t *testing.T, s *Server, path string) (*http.Cookie, string) {
	t.Helper()
	rec := get(t, s, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", path, rec.Code)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			cookie = c
		}
	}
	m := tokenRe.FindStringSubmatch(rec.Body.String())
	if cookie == nil || m == nil {
		t.Fatalf("no CSRF cookie/token on %s", path)
	}
	if m[1] != cookie.Value {
		t.Fatalf("token in form does not match cookie")
	}
	return cookie, m[1]
}

func post(t *testing.T, s *Server, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// samplePDF is a minimal file that passes the server's PDF check.
var samplePDF = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")

// postSubmission sends form to /submission as multipart/form-data, the way
// a browser does. paper is attached as the "paper" file unless both
// paperName and paper are empty (a browser sends an empty part with an
// empty file name when no file was chosen).
func postSubmission(t *testing.T, s *Server, form url.Values, paperName string, paper []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartBody(t, form, paperName, paper)
	req := httptest.NewRequest(http.MethodPost, "/submission", body)
	req.Header.Set("Content-Type", contentType)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// multipartBody encodes form and paper as multipart/form-data and returns
// the body and its Content-Type.
func multipartBody(t *testing.T, form url.Values, paperName string, paper []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, vs := range form {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if paperName != "" || paper != nil {
		fw, err := mw.CreateFormFile("paper", paperName)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(paper)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

// storedPapers lists the files in the papers directory, temporary ones
// included.
func storedPapers(t *testing.T, s *Server) []string {
	t.Helper()
	entries, err := os.ReadDir(s.papersDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestEveryPageRenders(t *testing.T) {
	s := newTestServer(t, nil)
	if len(s.pages) < 30 {
		t.Fatalf("only %d pages registered", len(s.pages))
	}
	for _, p := range s.pages {
		if p.Path == "/admin" {
			continue
		}
		rec := get(t, s, p.Path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d", p.Path, rec.Code)
			continue
		}
		body := rec.Body.String()
		for _, bad := range []string{"<no value>", "ZgotmplZ", "%!"} {
			if strings.Contains(body, bad) {
				t.Errorf("GET %s: body contains %q", p.Path, bad)
			}
		}
		if !p.Home && !strings.Contains(body, "<h1>"+template_escape(p.Title)+"</h1>") {
			t.Errorf("GET %s: missing page heading %q", p.Path, p.Title)
		}
		if !strings.Contains(body, `<link rel="canonical" href="http://example.com`+p.Path+`">`) {
			t.Errorf("GET %s: canonical link missing", p.Path)
		}
	}
}

// template_escape mirrors html/template's escaping of the few characters
// that occur in page titles.
func template_escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "'", "&#39;", `"`, "&#34;").Replace(s)
}

func TestNotFoundAndTrailingSlash(t *testing.T) {
	s := newTestServer(t, nil)
	rec := get(t, s, "/no-such-page")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "We could not find that page") {
		t.Fatalf("404 page: code %d", rec.Code)
	}
	rec = get(t, s, "/call-for-papers/")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/call-for-papers" {
		t.Fatalf("trailing slash redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestStaticAssets(t *testing.T) {
	s := newTestServer(t, nil)
	rec := get(t, s, "/static/css/site.css?v=abc")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("css: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("versioned asset Cache-Control = %q", cc)
	}
	for _, p := range []string{"/static/", "/static/img/", "/static/../content/site.json", "/static/nope.css"} {
		if rec := get(t, s, p); rec.Code == http.StatusOK {
			t.Errorf("GET %s = 200, want an error", p)
		}
	}
	rec = get(t, s, "/static/files/ICAS-Call-for-Papers.pptx")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("pptx download: %d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
}

func TestEveryLinkedAssetExists(t *testing.T) {
	s := newTestServer(t, nil)
	assetRe := regexp.MustCompile(`(?:href|src)="(/static/[^"?#]+)`)
	seen := map[string]bool{}
	for _, p := range s.pages {
		if p.Path == "/admin" {
			continue
		}
		for _, m := range assetRe.FindAllStringSubmatch(get(t, s, p.Path).Body.String(), -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if rec := get(t, s, m[1]); rec.Code != http.StatusOK {
				t.Errorf("%s links to %s which returns %d", p.Path, m[1], rec.Code)
			}
		}
	}
}

func TestEveryInternalLinkResolves(t *testing.T) {
	s := newTestServer(t, nil)
	linkRe := regexp.MustCompile(`href="(/[^"#?]*)`)
	for _, p := range s.pages {
		if p.Path == "/admin" {
			continue
		}
		for _, m := range linkRe.FindAllStringSubmatch(get(t, s, p.Path).Body.String(), -1) {
			target := m[1]
			if strings.HasPrefix(target, "/static/") || target == "/manifest.webmanifest" || target == "/favicon.ico" || target == "/sitemap.xml" {
				continue
			}
			if _, ok := s.byPath[target]; !ok {
				t.Errorf("%s links to unknown page %s", p.Path, target)
			}
		}
	}
}

func TestEveryIconExists(t *testing.T) {
	s := newTestServer(t, nil)
	sprite, err := os.ReadFile(filepath.Join("..", "..", "web", "static", "img", "icons.svg"))
	if err != nil {
		t.Fatal(err)
	}
	useRe := regexp.MustCompile(`icons\.svg\?v=[^#"]*#([a-z0-9-]+)`)
	seen := map[string]bool{}
	for _, p := range s.pages {
		if p.Path == "/admin" {
			continue
		}
		for _, m := range useRe.FindAllStringSubmatch(get(t, s, p.Path).Body.String(), -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			if !bytes.Contains(sprite, []byte(`<symbol id="`+m[1]+`"`)) {
				t.Errorf("%s uses icon %q, which is not in icons.svg", p.Path, m[1])
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no icons found: the pattern is out of date")
	}
}

func TestSecurityHeadersAndGzip(t *testing.T) {
	s := newTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	h := rec.Header()
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("CSP = %q", h.Get("Content-Security-Policy"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Encoding") != "gzip" {
		t.Errorf("headers: nosniff=%q encoding=%q", h.Get("X-Content-Type-Options"), h.Get("Content-Encoding"))
	}
	if strings.Contains(rec.Body.String(), "style=\"") {
		t.Error("home page has an inline style attribute, which the CSP blocks")
	}
}

func validWithWorkshop() url.Values {
	return url.Values{
		"track":               {"with-workshop"},
		"title":               {"Teaching drones to land with model predictive control"},
		"abstract":            {strings.Repeat("word ", 120)},
		"topics":              {"Control of Autonomous Systems", "Autonomous Vehicles and Mobility", "Not a real topic"},
		"talk_outline":        {"0-5 min motivation; 5-20 min method; 20-25 min preview"},
		"workshop_plan":       {"M1 environment; M2 baseline; M3 MPC; M4 evaluation"},
		"software":            {"Python 3.11, ROS 2, Gazebo"},
		"target_os":           {"Ubuntu 22.04"},
		"hardware":            {"Laptop only"},
		"skill_level":         {"Intermediate"},
		"workshop_size":       {"25"},
		"laptop_requirements": {"4-core CPU, 16 GB RAM, 20 GB free disk, Ubuntu 22.04 or WSL2"},
		"ta_count":            {"1"},
		"ta_names":            {"Grace Hopper"},
		"demo_video":          {"https://youtu.be/example"},
		"speaker_name":        {"Ada Lovelace"},
		"speaker_email":       {"ada@example.org"},
		"affiliation":         {"Analytical Engines Ltd"},
		"bio":                 {strings.Repeat("bio ", 100)},
		"experience":          {"Several workshops"},
		"confirm_english":     {"yes"},
		"confirm_template":    {"yes"},
		"confirm_privacy":     {"yes"},
	}
}

func TestSubmissionFlow(t *testing.T) {
	s := newTestServer(t, nil)
	cookie, token := session(t, s, "/submission")
	page := get(t, s, "/submission").Body.String()
	for _, want := range []string{
		`enctype="multipart/form-data"`,
		`type="file" id="f-paper" name="paper" accept="application/pdf,.pdf" required data-max-bytes="10485760"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("submission form lacks %s", want)
		}
	}

	if n := strings.Count(page, `<h3><a href="/call-for-papers/with-workshop">`); n != 1 {
		t.Errorf("submission page shows the paper with workshop call %d times, want once", n)
	}
	for _, want := range []string{"(choose one)", `value="Other" id="f-topic-other"`, "<strong>Others</strong>"} {
		if !strings.Contains(page, want) {
			t.Errorf("submission form lacks %s", want)
		}
	}

	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	rec := postSubmission(t, s, form, "drones.pdf", samplePDF, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /submission = %d; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/submission/received?id=ICAS2027-W001" {
		t.Fatalf("Location = %q", loc)
	}

	theory := validWithWorkshop()
	theory.Set("track", "without-workshop")
	for _, k := range []string{"workshop_plan", "demo_video", "workshop_size", "laptop_requirements", "ta_count"} {
		theory.Del(k)
	}
	theory.Set(csrfFieldName, token)
	rec = postSubmission(t, s, theory, `C:\fakepath\Theory Paper.PDF`, samplePDF, cookie)
	if loc := rec.Header().Get("Location"); loc != "/submission/received?id=ICAS2027-P001" {
		t.Fatalf("paper without workshop: Location = %q (code %d)", loc, rec.Code)
	}

	raw, _ := s.store.List("submissions")
	subs, err := store.Decode[Submission](raw)
	if err != nil || len(subs) != 2 {
		t.Fatalf("stored %d submissions, err %v", len(subs), err)
	}
	if got := subs[0].Topics; len(got) != 2 {
		t.Errorf("unknown topic was not dropped: %v", got)
	}
	if subs[0].WorkshopSize != "25" || subs[0].Laptop == "" || subs[0].TACount != "1" || subs[0].TANames != "Grace Hopper" {
		t.Errorf("workshop fields not stored: %+v", subs[0])
	}
	if subs[1].DemoVideo != "" || subs[1].WorkshopPlan != "" || subs[1].TANames != "" {
		t.Errorf("paper without workshop kept workshop fields: %+v", subs[1])
	}

	sum := sha256.Sum256(samplePDF)
	for i, want := range []struct{ file, name string }{
		{"ICAS2027-W001.pdf", "drones.pdf"},
		{"ICAS2027-P001.pdf", "Theory Paper.PDF"},
	} {
		got := subs[i]
		if got.PaperFile != want.file || got.PaperName != want.name ||
			got.PaperSize != int64(len(samplePDF)) || got.PaperSHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("paper %d stored as %q %q %d %q", i, got.PaperFile, got.PaperName, got.PaperSize, got.PaperSHA256)
		}
		data, err := os.ReadFile(filepath.Join(s.papersDir(), want.file))
		if err != nil || !bytes.Equal(data, samplePDF) {
			t.Errorf("paper file %s: %v", want.file, err)
		}
	}
	if got := storedPapers(t, s); len(got) != 2 {
		t.Errorf("papers directory holds %v, want exactly the two PDFs", got)
	}

	rec = get(t, s, "/submission/received?id=ICAS2027-W001")
	if !strings.Contains(rec.Body.String(), "ICAS2027-W001") {
		t.Error("receipt page does not show the submission ID")
	}
	rec = get(t, s, "/submission/received?id=<script>")
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Error("receipt page reflects an unvalidated ID")
	}
}

func TestSubmissionValidation(t *testing.T) {
	s := newTestServer(t, nil)
	cookie, token := session(t, s, "/submission")

	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	form.Set("abstract", strings.Repeat("word ", 301))
	form.Set("bio", strings.Repeat("bio ", 151))
	form.Set("demo_video", "not a url")
	form.Del("software")
	form.Set("workshop_size", "30")
	form.Del("laptop_requirements")
	form.Set("ta_count", "0")
	form.Del("confirm_english")
	form.Del("confirm_template")
	rec := postSubmission(t, s, form, "drones.pdf", samplePDF, cookie)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"then select your PDF again",
		"Abstract must be at most 300 words (currently 301).",
		"Speaker bio must be at most 150 words (currently 151).",
		"Please enter a full link starting with https://",
		"Required software is required.",
		"Please choose a workshop for 25 or 50 participants.",
		"Laptop hardware requirements for participants is required.",
		"Please choose the number of teaching assistants (at least one).",
		"Please confirm that your proposal is written in English.",
		"Please confirm that you will use the official ICAS slide template.",
	} {
		if !strings.Contains(body, template_escape(want)) {
			t.Errorf("missing error %q", want)
		}
	}
	if !strings.Contains(body, "Teaching drones to land") {
		t.Error("submitted values were not kept in the re-rendered form")
	}
	if n, _ := s.store.Count("submissions"); n != 0 {
		t.Errorf("invalid submission was stored")
	}
	if got := storedPapers(t, s); len(got) != 0 {
		t.Errorf("paper of an invalid submission was kept: %v", got)
	}
}

func TestPaperUploadValidation(t *testing.T) {
	s := newTestServer(t, nil)
	cookie, token := session(t, s, "/submission")
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)

	big := append(append([]byte(nil), samplePDF...), bytes.Repeat([]byte{' '}, maxPaperBytes)...)
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"", nil, "Please upload your paper as a PDF file."},
		{"", []byte{}, "Please upload your paper as a PDF file."},
		{"paper.docx", samplePDF, "Please upload a PDF file (.pdf). Other formats are not accepted."},
		{"paper", samplePDF, "Please upload a PDF file (.pdf). Other formats are not accepted."},
		{"renamed.pdf", []byte("PK\x03\x04 a zip archive"), "The file is not a valid PDF document."},
		{"empty.pdf", []byte{}, "The file is not a valid PDF document."},
		{"big.pdf", big, "The PDF must be at most 10 MB."},
	} {
		rec := postSubmission(t, s, form, c.name, c.data, cookie)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("paper %q (%d bytes): code %d, want 422 with %q", c.name, len(c.data), rec.Code, c.want)
		}
	}

	// A form sent without multipart encoding cannot carry a file.
	if rec := post(t, s, "/submission", form, cookie); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "Please upload your paper as a PDF file.") {
		t.Errorf("URL-encoded submission: code %d, want 422", rec.Code)
	}

	// A request larger than the PDF limit plus the form is cut off.
	huge := append(append([]byte(nil), samplePDF...), bytes.Repeat([]byte{' '}, maxSubmissionBytes)...)
	if rec := postSubmission(t, s, form, "huge.pdf", huge, cookie); rec.Code != http.StatusRequestEntityTooLarge ||
		!strings.Contains(rec.Body.String(), "the paper must be a PDF of at most 10 MB") {
		t.Errorf("oversized request: code %d, want 413", rec.Code)
	}

	if n, _ := s.store.Count("submissions"); n != 0 {
		t.Errorf("%d rejected submissions were stored", n)
	}
	if got := storedPapers(t, s); len(got) != 0 {
		t.Errorf("rejected papers left files behind: %v", got)
	}
}

func TestSlowUploadOutlastsServerTimeouts(t *testing.T) {
	s := newTestServer(t, nil)
	ts := httptest.NewUnstartedServer(s)
	// Far shorter than the pause below: the upload only succeeds if the
	// handler extends the deadlines of its request.
	ts.Config.ReadTimeout = 300 * time.Millisecond
	ts.Config.WriteTimeout = 300 * time.Millisecond
	ts.Start()
	defer ts.Close()

	cookie, token := session(t, s, "/submission")
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	body, contentType := multipartBody(t, form, "drones.pdf", samplePDF)
	data := body.Bytes()

	// Send half of the body, stall like a slow connection, then the rest.
	pr, pw := io.Pipe()
	go func() {
		pw.Write(data[:len(data)/2])
		time.Sleep(time.Second)
		pw.Write(data[len(data)/2:])
		pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/submission", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept-Encoding", "gzip") // as browsers send it, which puts the gzip writer in the chain
	req.AddCookie(cookie)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("slow upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("slow upload = %d, want 303", resp.StatusCode)
	}
}

func TestOtherTopic(t *testing.T) {
	s := newTestServer(t, nil)
	cookie, token := session(t, s, "/submission")

	// Ticking Others needs a description.
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	form.Add("topics", "Other")
	rec := postSubmission(t, s, form, "drones.pdf", samplePDF, cookie)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), template_escape("Please describe your topic, or untick “Others”.")) {
		t.Fatalf("Others without a description: code %d", rec.Code)
	}

	// A description counts as ticking Others.
	form.Del("topics")
	form.Set("other_topic", "Underwater docking")
	if rec := postSubmission(t, s, form, "drones.pdf", samplePDF, cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("description without Others: code %d", rec.Code)
	}
	raw, _ := s.store.List("submissions")
	subs, err := store.Decode[Submission](raw)
	if err != nil || len(subs) != 1 {
		t.Fatalf("stored %d submissions, err %v", len(subs), err)
	}
	if got := subs[0].Topics; len(got) != 1 || got[0] != "Other" || subs[0].OtherTopic != "Underwater docking" {
		t.Errorf("topics = %v, other topic = %q", got, subs[0].OtherTopic)
	}
}

func TestSubmissionIDSkipsOrphanedPDF(t *testing.T) {
	s := newTestServer(t, nil)
	// The PDF of a record removed by hand must never be overwritten.
	if err := os.MkdirAll(s.papersDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(s.papersDir(), "ICAS2027-W001.pdf")
	if err := os.WriteFile(orphan, []byte("%PDF-1.4 orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	cookie, token := session(t, s, "/submission")
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	rec := postSubmission(t, s, form, "drones.pdf", samplePDF, cookie)
	if loc := rec.Header().Get("Location"); loc != "/submission/received?id=ICAS2027-W002" {
		t.Fatalf("Location = %q (code %d)", loc, rec.Code)
	}
	if data, _ := os.ReadFile(orphan); string(data) != "%PDF-1.4 orphan" {
		t.Error("orphaned PDF was overwritten")
	}
}

func TestCleanFileName(t *testing.T) {
	for in, want := range map[string]string{
		"paper.pdf":                "paper.pdf",
		`C:\fakepath\My Paper.pdf`: "My Paper.pdf",
		"../../etc/passwd.pdf":     "passwd.pdf",
		"a\x00b\r\nc.pdf":          "abc.pdf",
	} {
		if got := cleanFileName(in); got != want {
			t.Errorf("cleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCSRFAndHoneypot(t *testing.T) {
	s := newTestServer(t, nil)
	rec := postSubmission(t, s, validWithWorkshop(), "drones.pdf", samplePDF, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d, want 403", rec.Code)
	}

	cookie, token := session(t, s, "/submission")
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	form.Set("website", "http://spam.example")
	rec = postSubmission(t, s, form, "spam.pdf", samplePDF, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("honeypot POST = %d", rec.Code)
	}
	if n, _ := s.store.Count("submissions"); n != 0 {
		t.Error("honeypot submission was stored")
	}
	if got := storedPapers(t, s); len(got) != 0 {
		t.Errorf("honeypot submission left files behind: %v", got)
	}
}

func TestSubmissionClosed(t *testing.T) {
	s := newTestServer(t, nil)
	s.site().Conference.SubmissionOpen = false
	cookie, token := session(t, s, "/submission")
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	if rec := postSubmission(t, s, form, "drones.pdf", samplePDF, cookie); rec.Code != http.StatusConflict {
		t.Fatalf("POST while closed = %d, want 409", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	s := newTestServer(t, nil)
	cookie, token := session(t, s, "/contact")
	form := url.Values{"name": {"A"}, "email": {"a@example.org"}, "topic": {"Other"}, "message": {"hi"}, csrfFieldName: {token}}
	var last int
	for i := 0; i < 13; i++ {
		last = post(t, s, "/contact", form, cookie).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("13th POST = %d, want 429", last)
	}
}

func TestContactAndSubscribe(t *testing.T) {
	s := newTestServer(t, nil)
	cookie, token := session(t, s, "/contact")
	rec := post(t, s, "/contact", url.Values{
		"name": {"Grace"}, "email": {"grace@example.org"}, "topic": {"Registration"},
		"message": {"When does registration open?"}, csrfFieldName: {token},
	}, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/contact?sent=1" {
		t.Fatalf("contact POST = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(get(t, s, "/contact?sent=1").Body.String(), "your message has been sent") {
		t.Error("contact confirmation not shown")
	}

	sub := url.Values{"first_name": {"Alan"}, "last_name": {"Turing"}, "email": {"alan@example.org"},
		"work_field": {"Academia"}, "interests": {"Registration"}, "consent": {"yes"}, csrfFieldName: {token}}
	if loc := post(t, s, "/subscribe", sub, cookie).Header().Get("Location"); loc != "/subscribe?done=1" {
		t.Fatalf("first subscribe -> %q", loc)
	}
	sub.Set("email", "ALAN@example.org")
	if loc := post(t, s, "/subscribe", sub, cookie).Header().Get("Location"); loc != "/subscribe?done=already" {
		t.Fatalf("duplicate subscribe -> %q", loc)
	}
	if n, _ := s.store.Count("subscribers"); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}
	sub.Set("email", "x@example.org")
	sub.Del("consent")
	if rec := post(t, s, "/subscribe", sub, cookie); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("subscribe without consent = %d", rec.Code)
	}
}

func TestAdmin(t *testing.T) {
	s := newTestServer(t, nil)
	if rec := get(t, s, "/admin"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin without auth = %d", rec.Code)
	}

	cookie, token := session(t, s, "/submission")
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	form.Set("title", "=HYPERLINK(\"http://evil\")")
	if rec := postSubmission(t, s, form, "ada-lovelace-draft.pdf", samplePDF, cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("submission = %d", rec.Code)
	}

	auth := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetBasicAuth("admin", "s3cret")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	rec := auth("/admin")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ICAS2027-W001") {
		t.Fatalf("admin page: %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("admin Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(rec.Body.String(), `href="/admin/paper?id=ICAS2027-W001"`) {
		t.Error("admin page does not link the paper")
	}
	rec = auth("/admin/export?kind=submissions")
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") || !strings.Contains(rec.Body.String(), "ada@example.org") {
		t.Fatalf("submissions export: %s", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), `'=HYPERLINK`) {
		t.Error("formula injection not neutralised in CSV export")
	}
	if !strings.Contains(rec.Body.String(), "ada-lovelace-draft.pdf") || !strings.Contains(rec.Body.String(), "Paper SHA-256") {
		t.Error("submissions export lacks the paper details")
	}
	rec = auth("/admin/export?kind=review")
	if strings.Contains(rec.Body.String(), "ada@example.org") || strings.Contains(rec.Body.String(), "Ada Lovelace") ||
		strings.Contains(rec.Body.String(), "Grace Hopper") || strings.Contains(rec.Body.String(), "lovelace") {
		t.Error("reviewer export leaks speaker or TA identity")
	}
	if !strings.Contains(rec.Body.String(), "ICAS2027-W001.pdf") {
		t.Error("reviewer export does not name the paper file")
	}
	if !strings.Contains(rec.Body.String(), "Paper with Workshop") {
		t.Error("reviewer export does not use the track name")
	}
	if rec := auth("/admin/export?kind=nope"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown export kind = %d", rec.Code)
	}

	rec = auth("/admin/paper?id=ICAS2027-W001")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !bytes.Equal(rec.Body.Bytes(), samplePDF) {
		t.Fatalf("paper download: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="ICAS2027-W001.pdf"` {
		t.Errorf("paper Content-Disposition = %q", cd)
	}
	for _, id := range []string{"ICAS2027-W002", "../ICAS2027-W001", "../submissions", ""} {
		if rec := auth("/admin/paper?id=" + url.QueryEscape(id)); rec.Code != http.StatusNotFound {
			t.Errorf("paper %q = %d, want 404", id, rec.Code)
		}
	}
	if rec := get(t, s, "/admin/paper?id=ICAS2027-W001"); rec.Code != http.StatusUnauthorized {
		t.Errorf("paper without auth = %d, want 401", rec.Code)
	}

	rec = auth("/admin/papers.zip")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("papers zip: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "ICAS2027-W001.pdf" {
		t.Fatalf("papers zip content: %v", err)
	}
	zf, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(zf)
	zf.Close()
	if !bytes.Equal(data, samplePDF) {
		t.Error("papers zip holds a different PDF")
	}
	if rec := get(t, s, "/admin/papers.zip"); rec.Code != http.StatusUnauthorized {
		t.Errorf("papers zip without auth = %d, want 401", rec.Code)
	}
}

func TestAdminDisabledWithoutPassword(t *testing.T) {
	s := newTestServer(t, func(c *Config) { c.AdminPassword = "" })
	if rec := get(t, s, "/admin"); rec.Code != http.StatusNotFound {
		t.Fatalf("admin without configured password = %d, want 404", rec.Code)
	}
}

func TestRemovedAndMovedPages(t *testing.T) {
	s := newTestServer(t, nil)
	for _, p := range []string{
		"/venue", "/travel/city", "/travel/hotels", "/travel/getting-there", "/travel/visa", "/authors/travel-grants",
		"/call-for-papers/special-sessions", "/call-for-papers/cross-community", "/call-for-papers/tutorials",
		"/call-for-papers/demonstrations", "/call-for-papers/challenge",
		"/program/tutorials", "/program/special-events", "/program/events/industry-forum", "/program/social-events",
	} {
		if rec := get(t, s, p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
	for from, to := range movedPages {
		rec := get(t, s, from)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != to {
			t.Errorf("GET %s = %d -> %q, want 301 -> %s", from, rec.Code, rec.Header().Get("Location"), to)
		}
	}
	var top []string
	for _, item := range s.nav {
		top = append(top, item.Label)
	}
	if got := strings.Join(top, ", "); got != "Home, About, Call for Papers, Authors, Program, Registration, Sponsors & Exhibitors, Downloads, Contact" {
		t.Errorf("main menu = %s", got)
	}
}

func TestSpeakerKitAndDownloads(t *testing.T) {
	s := newTestServer(t, nil)
	for _, p := range []string{"/", "/call-for-papers", "/call-for-papers/with-workshop", "/call-for-papers/without-workshop", "/submission", "/authors"} {
		body := get(t, s, p).Body.String()
		for _, file := range []string{"/static/files/ICAS-Call-for-Papers.pptx", "/static/files/ICAS-slide-template.pptx"} {
			if !strings.Contains(body, `class="kit-item" href="`+file+`"`) {
				t.Errorf("%s: speaker kit lacks %s", p, file)
			}
		}
	}
	body := get(t, s, "/downloads").Body.String()
	for _, g := range s.site().Downloads {
		if !strings.Contains(body, `id="`+g.Key+`"`) {
			t.Errorf("downloads page has no section %q", g.Key)
		}
		for _, d := range g.Items {
			if d.URL != "" && !strings.Contains(body, `href="`+d.URL+`"`) {
				t.Errorf("downloads page does not link %s", d.URL)
			}
		}
	}
}

func TestSponsorPackages(t *testing.T) {
	s := newTestServer(t, nil)
	body := get(t, s, "/sponsors").Body.String()
	for _, want := range []string{"NT$5,000", "NT$10,000", "NT$20,000", "NT$30,000"} {
		if !strings.Contains(body, want) {
			t.Errorf("sponsor page lacks the %s package", want)
		}
	}
	if strings.Contains(body, ">Student<") {
		t.Error("sponsor page still offers a student package")
	}
	booth := s.site().Sponsorship.Benefits[len(s.site().Sponsorship.Benefits)-1]
	if booth.Label != "Exhibition booth" || strings.Join(booth.Values, ",") != "no,no,no,yes" {
		t.Errorf("exhibition booth row = %s %v, want it in the last package only", booth.Label, booth.Values)
	}
}

func TestSitemapRobotsManifest(t *testing.T) {
	s := newTestServer(t, nil)
	body := get(t, s, "/sitemap.xml").Body.String()
	for _, p := range s.pages {
		has := strings.Contains(body, "<loc>http://example.com"+p.Path+"</loc>")
		if has == p.NoIndex {
			t.Errorf("sitemap entry for %s: present=%v NoIndex=%v", p.Path, has, p.NoIndex)
		}
	}
	if !strings.Contains(get(t, s, "/robots.txt").Body.String(), "Sitemap: http://example.com/sitemap.xml") {
		t.Error("robots.txt has no sitemap line")
	}
	var manifest map[string]any
	if err := json.Unmarshal(get(t, s, "/manifest.webmanifest").Body.Bytes(), &manifest); err != nil {
		t.Errorf("manifest is not valid JSON: %v", err)
	}
}

func TestMenuHighlightsOneSection(t *testing.T) {
	s := newTestServer(t, nil)
	cases := map[string]string{
		"/sponsors":                     "/sponsors",
		"/call-for-papers/requirements": "/call-for-papers",
		"/program/keynotes":             "/program",
		"/submission":                   "/authors",
		"/committee/technical-program":  "/about",
		"/downloads":                    "/downloads",
	}
	for path, want := range cases {
		var active []string
		for _, v := range s.navFor(s.byPath[path]) {
			if v.Active {
				active = append(active, v.Path)
			}
		}
		if len(active) != 1 || active[0] != want {
			t.Errorf("%s: active menus %v, want [%s]", path, active, want)
		}
	}
}

func TestBreadcrumbs(t *testing.T) {
	s := newTestServer(t, nil)
	got := s.crumbsFor(s.byPath["/committee/technical-program"])
	want := []string{"Home", "About ICAS", "Organizing Committee", "Technical Program Committee"}
	if len(got) != len(want) {
		t.Fatalf("crumbs = %+v", got)
	}
	for i := range want {
		if got[i].Label != want[i] {
			t.Errorf("crumb %d = %q, want %q", i, got[i].Label, want[i])
		}
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "+x": "'+x", "-x": "'-x", "@x": "'@x", "plain": "plain", "": ""} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRateLimiterWindow(t *testing.T) {
	l := newRateLimiter(2, time.Minute)
	now := fixedNow
	if !l.allow("a", now) || !l.allow("a", now) || l.allow("a", now) {
		t.Fatal("limit of 2 not enforced")
	}
	if !l.allow("b", now) {
		t.Fatal("keys are not independent")
	}
	if !l.allow("a", now.Add(61*time.Second)) {
		t.Fatal("window did not slide")
	}
}
