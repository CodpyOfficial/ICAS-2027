package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"icas/internal/content"
	"icas/internal/store"
)

// Word limits from the call for papers.
var wordLimits = map[string]int{
	"abstract": 300,
	"bio":      150,
}

const (
	maxFormBytes  = 256 << 10
	maxShortField = 200
	maxLongField  = 6000
)

var (
	skillLevels   = []string{"Beginner", "Intermediate", "Advanced"}
	workshopSizes = []string{"25", "50"}
	taCounts      = []string{"1", "2", "3 or more"}
)

var contactTopics = []string{
	"General inquiry",
	"Paper submission",
	"Registration",
	"Sponsorship & exhibition",
	"Visa invitation letter",
	"Media & press",
	"Other",
}

var receiptPattern = regexp.MustCompile(`^[A-Z0-9]{2,16}-[WP][0-9]{3,5}$`)

// formState carries submitted values and validation errors back to a form.
type formState struct {
	Values  map[string]string
	Multi   map[string][]string
	Errors  map[string]string
	Notice  string // page-level success message
	Problem string // page-level error message
}

func newForm() *formState {
	return &formState{Values: map[string]string{}, Multi: map[string][]string{}, Errors: map[string]string{}}
}

func (f *formState) fail(field, msg string) {
	if _, exists := f.Errors[field]; !exists {
		f.Errors[field] = msg
	}
}

func (f *formState) ok() bool { return len(f.Errors) == 0 && f.Problem == "" }

func countWords(s string) int { return len(strings.Fields(s)) }

// collect copies and trims the named fields from the request into f.
func (f *formState) collect(r *http.Request, names ...string) {
	for _, n := range names {
		f.Values[n] = strings.TrimSpace(strings.ReplaceAll(r.PostFormValue(n), "\r\n", "\n"))
	}
}

func (f *formState) required(name, label string) bool {
	if f.Values[name] == "" {
		f.fail(name, label+" is required.")
		return false
	}
	return true
}

func (f *formState) maxLen(name, label string, n int) {
	if utf8.RuneCountInString(f.Values[name]) > n {
		f.fail(name, fmt.Sprintf("%s must be at most %d characters.", label, n))
	}
}

func (f *formState) maxWords(name, label string) {
	if limit := wordLimits[name]; countWords(f.Values[name]) > limit {
		f.fail(name, fmt.Sprintf("%s must be at most %d words (currently %d).", label, limit, countWords(f.Values[name])))
	}
}

func (f *formState) email(name string) {
	v := f.Values[name]
	if v == "" {
		return
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v || !strings.Contains(v[strings.LastIndex(v, "@")+1:], ".") {
		f.fail(name, "Please enter a valid email address.")
	}
}

func (f *formState) webURL(name string) {
	v := f.Values[name]
	if v == "" {
		return
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		f.fail(name, "Please enter a full link starting with https://")
	}
}

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// prepare enforces the shared POST preconditions: size limit, parsing
// (URL-encoded or multipart), CSRF and rate limiting. tooLarge is shown
// when the body exceeds limit. It returns false after writing a response.
func (s *Server) prepare(w http.ResponseWriter, r *http.Request, page *Page, f *formState, limit int64, tooLarge string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		err = r.ParseMultipartForm(1 << 20) // larger parts are buffered in temporary files
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || strings.Contains(err.Error(), "request body too large") {
			f.Problem = tooLarge
			s.render(w, r, http.StatusRequestEntityTooLarge, page, f, nil)
			return false
		}
		f.Problem = "The form could not be read. Please try again."
		s.render(w, r, http.StatusBadRequest, page, f, nil)
		return false
	}
	if !validCSRF(r) {
		f.Problem = "Your session expired before the form was sent. Your answers are kept below — please submit again."
		s.render(w, r, http.StatusForbidden, page, f, nil)
		return false
	}
	if !s.forms.allow(clientIP(r, s.cfg.TrustProxy), s.now()) {
		f.Problem = "Too many submissions from your network in a short time. Please wait a few minutes and try again."
		s.render(w, r, http.StatusTooManyRequests, page, f, nil)
		return false
	}
	return true
}

// honeypot reports whether the hidden anti-spam field was filled in.
func honeypot(r *http.Request) bool { return strings.TrimSpace(r.PostFormValue("website")) != "" }

// ---------------- proposal submission ----------------

// Submission is one stored proposal.
type Submission struct {
	ID           string    `json:"id"`
	Received     time.Time `json:"received"`
	Track        string    `json:"track"`
	Title        string    `json:"title"`
	Abstract     string    `json:"abstract"`
	Topics       []string  `json:"topics"`
	OtherTopic   string    `json:"otherTopic,omitempty"`
	TalkOutline  string    `json:"talkOutline"`
	WorkshopPlan string    `json:"workshopPlan,omitempty"`
	Software     string    `json:"software,omitempty"`
	TargetOS     string    `json:"targetOS,omitempty"`
	Hardware     string    `json:"hardware,omitempty"` // equipment the speakers provide
	SkillLevel   string    `json:"skillLevel,omitempty"`
	WorkshopSize string    `json:"workshopSize,omitempty"` // 25 or 50 participants
	Laptop       string    `json:"laptopRequirements,omitempty"`
	TACount      string    `json:"taCount,omitempty"`
	TANames      string    `json:"taNames,omitempty"`
	DemoVideo    string    `json:"demoVideo,omitempty"`
	PaperFile    string    `json:"paperFile"` // <ID>.pdf in the papers directory
	PaperName    string    `json:"paperName"` // the file name on the author's computer
	PaperSize    int64     `json:"paperSize"`
	PaperSHA256  string    `json:"paperSHA256"`
	SpeakerName  string    `json:"speakerName"`
	SpeakerEmail string    `json:"speakerEmail"`
	Affiliation  string    `json:"affiliation"`
	Country      string    `json:"country,omitempty"`
	CoSpeakers   string    `json:"coSpeakers,omitempty"`
	Bio          string    `json:"bio"`
	Experience   string    `json:"experience"`
	Links        string    `json:"links,omitempty"`
	IP           string    `json:"ip"`
	UserAgent    string    `json:"userAgent"`
}

var submissionFields = []string{
	"track", "title", "abstract", "other_topic", "talk_outline", "workshop_plan",
	"software", "target_os", "hardware", "skill_level", "workshop_size", "laptop_requirements",
	"ta_count", "ta_names", "demo_video",
	"speaker_name", "speaker_email", "affiliation", "country", "co_speakers",
	"bio", "experience", "links", "confirm_english", "confirm_template", "confirm_privacy",
}

func (s *Server) handleSubmission(w http.ResponseWriter, r *http.Request) {
	page := s.byPath["/submission"]
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		f := newForm()
		if t := r.URL.Query().Get("track"); t == content.TrackWithWorkshop || t == content.TrackWithoutWorkshop {
			f.Values["track"] = t
		}
		s.render(w, r, http.StatusOK, page, f, nil)
	case http.MethodPost:
		allowSlowTransfer(w) // before the body is read: it carries the PDF
		f := newForm()
		defer func() {
			if r.MultipartForm != nil {
				r.MultipartForm.RemoveAll()
			}
		}()
		tooLarge := fmt.Sprintf("The submission is too large: the paper must be a PDF of at most %d MB. Please select a smaller file and fill in the form again.", maxPaperBytes>>20)
		if !s.prepare(w, r, page, f, maxSubmissionBytes, tooLarge) {
			return
		}
		site := s.site()
		f.collect(r, submissionFields...)
		f.Multi["topics"] = r.PostForm["topics"]
		if !site.Conference.SubmissionOpen {
			f.Problem = "The submission system is not open at the moment. Please check the Important Dates and try again once submissions open."
			s.render(w, r, http.StatusConflict, page, f, nil)
			return
		}
		if honeypot(r) {
			http.Redirect(w, r, "/submission/received", http.StatusSeeOther)
			return
		}
		sub := validateSubmission(f, site)
		// The paper is checked last and only kept when the whole form is valid.
		paper, err := s.receivePaper(r, f)
		if err != nil {
			s.serverError(w, fmt.Errorf("store uploaded paper: %w", err))
			return
		}
		if !f.ok() {
			f.Problem = "Please correct the highlighted fields below, then select your PDF again: browsers do not keep files when a form is returned."
			s.render(w, r, http.StatusUnprocessableEntity, page, f, nil)
			return
		}
		sub.Received = s.now().UTC()
		sub.IP = clientIP(r, s.cfg.TrustProxy)
		sub.UserAgent = truncate(r.UserAgent(), 300)
		prefix := strings.ToUpper(site.Conference.Acronym + site.Conference.Year)
		letter := "P" // paper without workshop
		if sub.Track == content.TrackWithWorkshop {
			letter = "W"
		}
		var stored string
		err = s.store.Add("submissions", func(existing []json.RawMessage) (any, error) {
			subs, err := store.Decode[Submission](existing)
			if err != nil {
				return nil, err
			}
			sub.ID = s.nextID(subs, sub.Track, prefix+"-"+letter)
			// The PDF is named after the submission ID, so the stored file
			// name carries no author information.
			target := filepath.Join(s.papersDir(), sub.ID+".pdf")
			if err := os.Rename(paper.tmpPath, target); err != nil {
				return nil, err
			}
			stored = target
			sub.PaperFile, sub.PaperName = sub.ID+".pdf", paper.name
			sub.PaperSize, sub.PaperSHA256 = paper.size, paper.sha256
			return sub, nil
		})
		if err != nil {
			os.Remove(paper.tmpPath)
			if stored != "" {
				os.Remove(stored)
			}
			s.serverError(w, fmt.Errorf("save submission: %w", err))
			return
		}
		s.logger.Printf("submission %s received (%s)", sub.ID, sub.Track)
		http.Redirect(w, r, "/submission/received?id="+url.QueryEscape(sub.ID), http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func validateSubmission(f *formState, site *content.Site) Submission {
	v := f.Values
	track := v["track"]
	if !oneOf(track, []string{content.TrackWithWorkshop, content.TrackWithoutWorkshop}) {
		f.fail("track", "Please choose Paper with Workshop or Paper without Workshop.")
	}
	f.required("title", "Title")
	f.maxLen("title", "Title", maxShortField)
	f.required("abstract", "Abstract")
	f.maxWords("abstract", "Abstract")
	f.maxLen("abstract", "Abstract", maxLongField)

	valid := make([]string, 0, len(site.Topics)+1)
	for _, t := range site.Topics {
		valid = append(valid, t.Title)
	}
	valid = append(valid, "Other")
	var topics []string
	for _, t := range f.Multi["topics"] {
		if oneOf(t, valid) && !oneOf(t, topics) {
			topics = append(topics, t)
		}
	}
	f.Multi["topics"] = topics
	if oneOf("Other", topics) && v["other_topic"] == "" {
		f.fail("other_topic", "Please describe the topic, or untick “Other”.")
	}
	f.maxLen("other_topic", "Topic description", maxShortField)

	f.required("talk_outline", "Presentation outline")
	f.maxLen("talk_outline", "Presentation outline", maxLongField)

	if track == content.TrackWithWorkshop {
		f.required("workshop_plan", "Workshop plan")
		f.maxLen("workshop_plan", "Workshop plan", maxLongField)
		f.required("software", "Required software")
		f.maxLen("software", "Required software", 2000)
		f.required("target_os", "Target OS compatibility")
		f.maxLen("target_os", "Target OS compatibility", maxShortField)
		f.required("hardware", "Equipment provided by the speakers")
		f.maxLen("hardware", "Equipment provided by the speakers", 2000)
		if f.required("skill_level", "Recommended skill level") && !oneOf(v["skill_level"], skillLevels) {
			f.fail("skill_level", "Please choose a skill level from the list.")
		}
		if f.required("workshop_size", "Workshop size") && !oneOf(v["workshop_size"], workshopSizes) {
			f.fail("workshop_size", "Please choose a workshop for 25 or 50 participants.")
		}
		f.required("laptop_requirements", "Laptop hardware requirements for participants")
		f.maxLen("laptop_requirements", "Laptop hardware requirements", 2000)
		if f.required("ta_count", "Number of teaching assistants") && !oneOf(v["ta_count"], taCounts) {
			f.fail("ta_count", "Please choose the number of teaching assistants (at least one).")
		}
		f.maxLen("ta_names", "Teaching assistants", 1000)
		if f.required("demo_video", "Demo video link") {
			f.webURL("demo_video")
		}
		f.maxLen("demo_video", "Demo video link", 500)
	}

	f.required("speaker_name", "Speaker name")
	f.maxLen("speaker_name", "Speaker name", maxShortField)
	if f.required("speaker_email", "Email") {
		f.email("speaker_email")
	}
	f.maxLen("speaker_email", "Email", maxShortField)
	f.required("affiliation", "Affiliation")
	f.maxLen("affiliation", "Affiliation", maxShortField)
	f.maxLen("country", "Country", 100)
	f.maxLen("co_speakers", "Co-speakers", 2000)
	f.required("bio", "Speaker bio")
	f.maxWords("bio", "Speaker bio")
	f.maxLen("bio", "Speaker bio", 3000)
	f.required("experience", "Previous speaking experience")
	f.maxLen("experience", "Previous speaking experience", 3000)
	f.maxLen("links", "Project links", 2000)
	if v["confirm_english"] == "" {
		f.fail("confirm_english", "Please confirm that your proposal is written in English.")
	}
	if v["confirm_template"] == "" {
		f.fail("confirm_template", "Please confirm that you will use the official ICAS slide template.")
	}
	if v["confirm_privacy"] == "" {
		f.fail("confirm_privacy", "Please agree to the processing of your data for the review.")
	}

	sub := Submission{
		Track: track, Title: v["title"], Abstract: v["abstract"], Topics: topics,
		OtherTopic: v["other_topic"], TalkOutline: v["talk_outline"],
		SpeakerName: v["speaker_name"], SpeakerEmail: v["speaker_email"],
		Affiliation: v["affiliation"], Country: v["country"], CoSpeakers: v["co_speakers"],
		Bio: v["bio"], Experience: v["experience"], Links: v["links"],
	}
	if track == content.TrackWithWorkshop {
		sub.WorkshopPlan, sub.Software, sub.TargetOS = v["workshop_plan"], v["software"], v["target_os"]
		sub.Hardware, sub.SkillLevel, sub.DemoVideo = v["hardware"], v["skill_level"], v["demo_video"]
		sub.WorkshopSize, sub.Laptop = v["workshop_size"], v["laptop_requirements"]
		sub.TACount, sub.TANames = v["ta_count"], v["ta_names"]
	}
	return sub
}

func (s *Server) handleSubmissionReceived(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !receiptPattern.MatchString(id) {
		id = ""
	}
	s.render(w, r, http.StatusOK, s.byPath["/submission/received"], nil, id)
}

// ---------------- contact ----------------

type ContactMessage struct {
	Received time.Time `json:"received"`
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Topic    string    `json:"topic"`
	Subject  string    `json:"subject"`
	Message  string    `json:"message"`
	IP       string    `json:"ip"`
}

func (s *Server) handleContact(w http.ResponseWriter, r *http.Request) {
	page := s.byPath["/contact"]
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		f := newForm()
		if r.URL.Query().Get("sent") == "1" {
			f.Notice = "Thank you — your message has been sent to the organizing committee. We usually reply within a few working days."
		}
		if t := r.URL.Query().Get("topic"); oneOf(t, contactTopics) {
			f.Values["topic"] = t
		}
		s.render(w, r, http.StatusOK, page, f, contactTopics)
	case http.MethodPost:
		f := newForm()
		if !s.prepare(w, r, page, f, maxFormBytes, "The message is too long. Please shorten it and try again.") {
			return
		}
		f.collect(r, "name", "email", "topic", "subject", "message")
		if honeypot(r) {
			http.Redirect(w, r, "/contact?sent=1", http.StatusSeeOther)
			return
		}
		f.required("name", "Name")
		f.maxLen("name", "Name", maxShortField)
		if f.required("email", "Email") {
			f.email("email")
		}
		f.maxLen("email", "Email", maxShortField)
		if !oneOf(f.Values["topic"], contactTopics) {
			f.fail("topic", "Please choose a topic.")
		}
		f.maxLen("subject", "Subject", maxShortField)
		f.required("message", "Message")
		f.maxLen("message", "Message", maxLongField)
		if !f.ok() {
			f.Problem = "Please correct the highlighted fields below."
			s.render(w, r, http.StatusUnprocessableEntity, page, f, contactTopics)
			return
		}
		msg := ContactMessage{
			Received: s.now().UTC(), Name: f.Values["name"], Email: f.Values["email"],
			Topic: f.Values["topic"], Subject: f.Values["subject"], Message: f.Values["message"],
			IP: clientIP(r, s.cfg.TrustProxy),
		}
		if err := s.store.Add("contacts", func([]json.RawMessage) (any, error) { return msg, nil }); err != nil {
			s.serverError(w, fmt.Errorf("save contact message: %w", err))
			return
		}
		http.Redirect(w, r, "/contact?sent=1", http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// ---------------- newsletter ("Keep me updated!") ----------------

type Subscriber struct {
	Subscribed   time.Time `json:"subscribed"`
	FirstName    string    `json:"firstName"`
	LastName     string    `json:"lastName"`
	Email        string    `json:"email"`
	Organization string    `json:"organization"`
	Country      string    `json:"country"`
	WorkField    string    `json:"workField"`
	Interests    []string  `json:"interests,omitempty"`
}

var (
	interestOptions = []string{"Call for papers & deadlines", "Registration", "Program & speakers", "Sponsorship & exhibition"}
	workFields      = []string{"Academia", "Industry", "Research institute", "Government / public sector", "Other"}
)

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	page := s.byPath["/subscribe"]
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		f := newForm()
		switch r.URL.Query().Get("done") {
		case "1":
			f.Notice = "You are on the list. We will email you when deadlines, registration and the program are announced."
		case "already":
			f.Notice = "This address is already subscribed — no further action is needed."
		}
		s.render(w, r, http.StatusOK, page, f, interestOptions)
	case http.MethodPost:
		f := newForm()
		if !s.prepare(w, r, page, f, maxFormBytes, "The form is too large. Please shorten your answers and try again.") {
			return
		}
		f.collect(r, "first_name", "last_name", "email", "organization", "country", "work_field", "consent")
		var interests []string
		for _, v := range r.PostForm["interests"] {
			if oneOf(v, interestOptions) && !oneOf(v, interests) {
				interests = append(interests, v)
			}
		}
		f.Multi["interests"] = interests
		if honeypot(r) {
			http.Redirect(w, r, "/subscribe?done=1", http.StatusSeeOther)
			return
		}
		f.required("first_name", "First name")
		f.maxLen("first_name", "First name", 100)
		f.required("last_name", "Last name")
		f.maxLen("last_name", "Last name", 100)
		if f.required("email", "Email") {
			f.email("email")
		}
		f.maxLen("email", "Email", maxShortField)
		f.maxLen("organization", "Company / institution", maxShortField)
		f.maxLen("country", "Country", 100)
		if v := f.Values["work_field"]; v != "" && !oneOf(v, workFields) {
			f.fail("work_field", "Please choose a work field from the list.")
		}
		if f.Values["consent"] == "" {
			f.fail("consent", "Please agree to receive conference updates by email.")
		}
		if !f.ok() {
			f.Problem = "Please correct the highlighted fields below."
			s.render(w, r, http.StatusUnprocessableEntity, page, f, interestOptions)
			return
		}
		sub := Subscriber{Subscribed: s.now().UTC(), FirstName: f.Values["first_name"], LastName: f.Values["last_name"],
			Email: f.Values["email"], Organization: f.Values["organization"], Country: f.Values["country"],
			WorkField: f.Values["work_field"], Interests: interests}
		err := s.store.Add("subscribers", func(existing []json.RawMessage) (any, error) {
			subs, err := store.Decode[Subscriber](existing)
			if err != nil {
				return nil, err
			}
			for _, e := range subs {
				if strings.EqualFold(e.Email, sub.Email) {
					return nil, store.ErrSkip
				}
			}
			return sub, nil
		})
		switch {
		case errors.Is(err, store.ErrSkip):
			http.Redirect(w, r, "/subscribe?done=already", http.StatusSeeOther)
		case err != nil:
			s.serverError(w, fmt.Errorf("save subscriber: %w", err))
		default:
			http.Redirect(w, r, "/subscribe?done=1", http.StatusSeeOther)
		}
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
