package server

import (
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"icas/internal/content"
	"icas/internal/store"
)

// adminFailures throttles password guessing on the admin pages.
var adminFailures = newRateLimiter(10, 15*time.Minute)

// adminOK enforces HTTP basic auth. The admin area does not exist (404)
// unless a password is configured.
func (s *Server) adminOK(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.AdminPassword == "" {
		s.notFound(w, r)
		return false
	}
	ip := clientIP(r, s.cfg.TrustProxy)
	user, pass, ok := r.BasicAuth()
	if ok && subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.AdminUser)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.AdminPassword)) == 1 {
		return true
	}
	if ok && !adminFailures.allow(ip, s.now()) {
		http.Error(w, "Too many failed attempts; try again later.", http.StatusTooManyRequests)
		return false
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="ICAS admin", charset="UTF-8"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
	return false
}

type adminData struct {
	Submissions     []Submission
	Contacts        []ContactMessage
	Subscribers     []Subscriber
	WithWorkshop    int
	WithoutWorkshop int
	DataDir         string
}

func (s *Server) loadAdminData() (adminData, error) {
	var d adminData
	var err error
	if d.Submissions, err = listDecoded[Submission](s.store, "submissions"); err != nil {
		return d, err
	}
	if d.Contacts, err = listDecoded[ContactMessage](s.store, "contacts"); err != nil {
		return d, err
	}
	if d.Subscribers, err = listDecoded[Subscriber](s.store, "subscribers"); err != nil {
		return d, err
	}
	for _, sub := range d.Submissions {
		if sub.Track == content.TrackWithWorkshop {
			d.WithWorkshop++
		} else {
			d.WithoutWorkshop++
		}
	}
	// Newest first is what organizers want to see.
	sort.SliceStable(d.Submissions, func(i, j int) bool { return d.Submissions[i].Received.After(d.Submissions[j].Received) })
	sort.SliceStable(d.Contacts, func(i, j int) bool { return d.Contacts[i].Received.After(d.Contacts[j].Received) })
	sort.SliceStable(d.Subscribers, func(i, j int) bool { return d.Subscribers[i].Subscribed.After(d.Subscribers[j].Subscribed) })
	d.DataDir = s.store.Dir()
	return d, nil
}

func listDecoded[T any](st *store.Store, collection string) ([]T, error) {
	raw, err := st.List(collection)
	if err != nil {
		return nil, err
	}
	return store.Decode[T](raw)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	d, err := s.loadAdminData()
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, s.byPath["/admin"], nil, d)
}

func (s *Server) handleAdminExport(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	d, err := s.loadAdminData()
	if err != nil {
		s.serverError(w, err)
		return
	}
	kind := r.URL.Query().Get("kind")
	var header []string
	var rows [][]string
	switch kind {
	case "submissions":
		header = []string{"ID", "Received (UTC)", "Track", "Title", "Abstract", "Topics", "Other topic",
			"Presentation outline", "Workshop plan", "Software", "Target OS", "Equipment provided", "Skill level",
			"Workshop size", "Participant laptop requirements", "TAs", "TA names", "Demo video",
			"Speaker", "Email", "Affiliation", "Country", "Co-speakers", "Bio", "Speaking experience", "Links"}
		for _, x := range d.Submissions {
			rows = append(rows, []string{x.ID, x.Received.Format(time.RFC3339), trackName(x.Track), x.Title, x.Abstract,
				strings.Join(x.Topics, "; "), x.OtherTopic, x.TalkOutline, x.WorkshopPlan, x.Software, x.TargetOS,
				x.Hardware, x.SkillLevel, x.WorkshopSize, x.Laptop, x.TACount, x.TANames, x.DemoVideo,
				x.SpeakerName, x.SpeakerEmail, x.Affiliation, x.Country, x.CoSpeakers, x.Bio, x.Experience, x.Links})
		}
	case "review":
		// Anonymized for double-blind review: everything about the speakers
		// (name, contact, affiliation, bio, links) is left out.
		header = []string{"ID", "Track", "Title", "Abstract", "Topics", "Other topic", "Presentation outline",
			"Workshop plan", "Software", "Target OS", "Equipment provided", "Skill level", "Workshop size",
			"Participant laptop requirements", "TAs", "Demo video"}
		for _, x := range d.Submissions {
			rows = append(rows, []string{x.ID, trackName(x.Track), x.Title, x.Abstract, strings.Join(x.Topics, "; "), x.OtherTopic,
				x.TalkOutline, x.WorkshopPlan, x.Software, x.TargetOS, x.Hardware, x.SkillLevel, x.WorkshopSize,
				x.Laptop, x.TACount, x.DemoVideo})
		}
	case "contacts":
		header = []string{"Received (UTC)", "Name", "Email", "Topic", "Subject", "Message"}
		for _, x := range d.Contacts {
			rows = append(rows, []string{x.Received.Format(time.RFC3339), x.Name, x.Email, x.Topic, x.Subject, x.Message})
		}
	case "subscribers":
		header = []string{"Subscribed (UTC)", "First name", "Last name", "Email", "Company / institution", "Country", "Work field", "Interests"}
		for _, x := range d.Subscribers {
			rows = append(rows, []string{x.Subscribed.Format(time.RFC3339), x.FirstName, x.LastName, x.Email,
				x.Organization, x.Country, x.WorkField, strings.Join(x.Interests, "; ")})
		}
	default:
		http.Error(w, "unknown export kind", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="icas-%s-%s.csv"`, kind, s.now().Format("20060102")))
	w.Write([]byte("\xEF\xBB\xBF")) // UTF-8 BOM so Excel detects the encoding
	cw := csv.NewWriter(w)
	cw.Write(header)
	for _, row := range rows {
		for i := range row {
			row[i] = csvSafe(row[i])
		}
		cw.Write(row)
	}
	cw.Flush()
}

// trackName turns a stored track key into the label used on the site.
func trackName(key string) string {
	switch key {
	case content.TrackWithWorkshop:
		return "Paper with Workshop"
	case content.TrackWithoutWorkshop:
		return "Paper without Workshop"
	}
	return key
}

// csvSafe neutralises spreadsheet formula injection in exported cells.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// prettyJSON is used by the admin template to show a full record.
func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(b)
}
