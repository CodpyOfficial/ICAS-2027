package server

import (
	"encoding/json"
	"io"
	"log"
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

func TestEveryPageRenders(t *testing.T) {
	s := newTestServer(t, nil)
	if len(s.pages) < 45 {
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

	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	rec := post(t, s, "/submission", form, cookie)
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
	rec = post(t, s, "/submission", theory, cookie)
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
	rec := post(t, s, "/submission", form, cookie)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
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
}

func TestCSRFAndHoneypot(t *testing.T) {
	s := newTestServer(t, nil)
	rec := post(t, s, "/submission", validWithWorkshop(), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d, want 403", rec.Code)
	}

	cookie, token := session(t, s, "/submission")
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	form.Set("website", "http://spam.example")
	rec = post(t, s, "/submission", form, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("honeypot POST = %d", rec.Code)
	}
	if n, _ := s.store.Count("submissions"); n != 0 {
		t.Error("honeypot submission was stored")
	}
}

func TestSubmissionClosed(t *testing.T) {
	s := newTestServer(t, nil)
	s.site().Conference.SubmissionOpen = false
	cookie, token := session(t, s, "/submission")
	form := validWithWorkshop()
	form.Set(csrfFieldName, token)
	if rec := post(t, s, "/submission", form, cookie); rec.Code != http.StatusConflict {
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
	post(t, s, "/submission", form, cookie)

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
	rec = auth("/admin/export?kind=submissions")
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") || !strings.Contains(rec.Body.String(), "ada@example.org") {
		t.Fatalf("submissions export: %s", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), `'=HYPERLINK`) {
		t.Error("formula injection not neutralised in CSV export")
	}
	rec = auth("/admin/export?kind=review")
	if strings.Contains(rec.Body.String(), "ada@example.org") || strings.Contains(rec.Body.String(), "Ada Lovelace") ||
		strings.Contains(rec.Body.String(), "Grace Hopper") {
		t.Error("reviewer export leaks speaker or TA identity")
	}
	if !strings.Contains(rec.Body.String(), "Paper with Workshop") {
		t.Error("reviewer export does not use the track name")
	}
	if rec := auth("/admin/export?kind=nope"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown export kind = %d", rec.Code)
	}
}

func TestAdminDisabledWithoutPassword(t *testing.T) {
	s := newTestServer(t, func(c *Config) { c.AdminPassword = "" })
	if rec := get(t, s, "/admin"); rec.Code != http.StatusNotFound {
		t.Fatalf("admin without configured password = %d, want 404", rec.Code)
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
		"/travel/visa":                     "/venue",
		"/sponsors":                        "/sponsors",
		"/call-for-papers/cross-community": "/call-for-papers",
		"/program/events/industry-forum":   "/program",
		"/submission":                      "/authors",
		"/committee/technical-program":     "/about",
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
	got := s.crumbsFor(s.byPath["/program/events/mentoring-program"])
	want := []string{"Home", "Program at a Glance", "Special Events & Forums", "Mentoring Program"}
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
