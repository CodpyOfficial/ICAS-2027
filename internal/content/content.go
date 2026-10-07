// Package content defines the conference data model. Everything that the
// organizers are expected to edit (dates, venue, people, fees, news, ...)
// lives in a single JSON document so the site can be updated without
// touching templates or Go code. Empty strings mean "not announced yet";
// templates render them as a "TBA" badge.
package content

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"
)

// Site is the root of content/site.json.
type Site struct {
	Conference       Conference      `json:"conference"`
	Contacts         []Contact       `json:"contacts"`
	Secretariats     []Secretariat   `json:"secretariats"`
	Social           []SocialLink    `json:"social"`
	ImportantDates   []KeyDate       `json:"importantDates"`
	News             []NewsItem      `json:"news"`
	Calls            []Call          `json:"calls"`
	Tracks           []Track         `json:"tracks"`
	Topics           []Topic         `json:"topics"`
	Requirements     []Requirement   `json:"submissionRequirements"`
	ReviewCriteria   []Criterion     `json:"reviewCriteria"`
	Committee        []PeopleGroup   `json:"committee"`
	ProgramCommittee []PeopleGroup   `json:"programCommittee"`
	Keynotes         []Speaker       `json:"keynotes"`
	Program          []ProgramDay    `json:"program"`
	Workshops        []Session       `json:"workshops"`
	Talks            []Talk          `json:"talks"`
	Registration     Registration    `json:"registration"`
	Sponsorship      Sponsorship     `json:"sponsorship"`
	Journals         []Journal       `json:"journals"`
	FAQ              []FAQGroup      `json:"faq"`
	Downloads        []DownloadGroup `json:"downloads"`
	History          []Edition       `json:"history"`
}

// Conference holds the identity and headline facts of this edition.
type Conference struct {
	Acronym     string `json:"acronym"`
	Name        string `json:"name"`
	Year        string `json:"year"`
	Tagline     string `json:"tagline"`
	Description string `json:"description"`
	Dates       string `json:"dates"`     // display form, e.g. "6–9 June 2027"; empty = TBA
	StartDate   string `json:"startDate"` // ISO date used for the countdown; optional
	City        string `json:"city"`
	Country     string `json:"country"`
	VenueName   string `json:"venueName"`
	Format      string `json:"format"`
	Language    string `json:"language"`
	TimeZone    string `json:"timeZone"`
	BaseURL     string `json:"baseURL"`
	// Status flags and external systems drive the calls to action.
	SubmissionOpen   bool   `json:"submissionOpen"`
	RegistrationOpen bool   `json:"registrationOpen"`
	RegistrationURL  string `json:"registrationURL"`
	Copyright        string `json:"copyright"`
}

// FullTitle is e.g. "ICAS 2027".
func (c Conference) FullTitle() string {
	return strings.TrimSpace(c.Acronym + " " + c.Year)
}

// Location joins city and country, or returns "" when neither is known.
func (c Conference) Location() string {
	parts := make([]string, 0, 2)
	for _, p := range []string{c.City, c.Country} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

type Contact struct {
	Label string `json:"label"`
	Email string `json:"email"`
	Phone string `json:"phone"`
	Note  string `json:"note"`
}

type Secretariat struct {
	Title   string   `json:"title"`
	Purpose string   `json:"purpose"`
	Name    string   `json:"name"`
	Address []string `json:"address"`
	Phone   string   `json:"phone"`
	Email   string   `json:"email"`
	URL     string   `json:"url"`
}

type SocialLink struct {
	Network string `json:"network"` // icon id: linkedin, x-logo, facebook, youtube, github
	Label   string `json:"label"`
	URL     string `json:"url"`
}

// KeyDate is one row of the "Important Dates" list.
type KeyDate struct {
	Label     string `json:"label"`
	Date      string `json:"date"`     // display form; empty = TBA
	Original  string `json:"original"` // the previous date when a deadline was extended
	ISO       string `json:"iso"`      // optional, enables "past" styling
	Note      string `json:"note"`
	Highlight bool   `json:"highlight"`
}

// Past reports whether the date is known and before now.
func (d KeyDate) Past(now time.Time) bool {
	t, ok := parseISO(d.ISO)
	return ok && now.After(t.Add(24*time.Hour))
}

type NewsItem struct {
	Date     string `json:"date"`
	Text     string `json:"text"`
	Link     string `json:"link"`
	LinkText string `json:"linkText"`
}

// Call is one type of contribution (shown as cards on the submission page).
type Call struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Path        string `json:"path"`      // the call's detail page
	SubmitURL   string `json:"submitURL"` // where proposals go; empty = not open yet
	Status      string `json:"status"`    // open, soon, closed
	Deadline    string `json:"deadline"`
}

// Keys of the two proposal tracks.
const (
	TrackWithWorkshop    = "with-workshop"
	TrackWithoutWorkshop = "without-workshop"
)

// Track is one of the two proposal tracks described in the call for papers.
type Track struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Slots    int      `json:"slots"`
	Icon     string   `json:"icon"`
	Summary  string   `json:"summary"`
	Format   string   `json:"format"`
	Duration string   `json:"duration"`
	Points   []string `json:"points"`
	Path     string   `json:"path"`
}

type Topic struct {
	Title    string `json:"title"`
	Keywords string `json:"keywords"`
	Icon     string `json:"icon"`
	// Practice marks implementation-oriented topics; they are listed first
	// and labelled to show the conference's emphasis on practice.
	Practice bool `json:"practice"`
}

// Requirement is one item of the proposal submission requirements.
type Requirement struct {
	Title   string   `json:"title"`
	Text    string   `json:"text"`
	Applies string   `json:"applies"` // "all", "with-workshop" or "without-workshop"
	Items   []string `json:"items"`
	Icon    string   `json:"icon"`
}

type Criterion struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Icon  string `json:"icon"`
}

type PeopleGroup struct {
	Title  string   `json:"title"`
	People []Person `json:"people"`
}

type Person struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	Affiliation string `json:"affiliation"`
	Country     string `json:"country"`
	Photo       string `json:"photo"`
	URL         string `json:"url"`
	Email       string `json:"email"`
}

// Initials returns up to two initials for avatar placeholders.
func (p Person) Initials() string { return initials(p.Name) }

type Speaker struct {
	Name        string `json:"name"`
	Position    string `json:"position"`
	Affiliation string `json:"affiliation"`
	Country     string `json:"country"`
	Photo       string `json:"photo"`
	URL         string `json:"url"`
	Talk        string `json:"talk"`
	Abstract    string `json:"abstract"`
	Bio         string `json:"bio"`
	When        string `json:"when"`
}

func (s Speaker) Initials() string { return initials(s.Name) }

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a name into an anchor id ("Ada Lovelace" -> "ada-lovelace").
func Slug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

type ProgramDay struct {
	Label string `json:"label"`
	Date  string `json:"date"`
	Note  string `json:"note"`
	Slots []Slot `json:"slots"`
}

type Slot struct {
	Time     string `json:"time"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Kind     string `json:"kind"` // session, keynote, break, social
	Room     string `json:"room"`
	Link     string `json:"link"`
	Parallel []Cell `json:"parallel"`
}

type Cell struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Link   string `json:"link"`
}

// Session is the workshop of a paper with workshop.
type Session struct {
	Code          string      `json:"code"`
	Title         string      `json:"title"`
	Status        string      `json:"status"` // "", cancelled, rescheduled
	Date          string      `json:"date"`
	Time          string      `json:"time"`
	Room          string      `json:"room"`
	Level         string      `json:"level"`
	Capacity      string      `json:"capacity"` // 25 or 50 participants, chosen by the speaker
	Prerequisites string      `json:"prerequisites"`
	Laptop        string      `json:"laptop"` // hardware requirements for participants' laptops
	Abstract      string      `json:"abstract"`
	Schedule      []AgendaRow `json:"schedule"`
	Instructors   []Speaker   `json:"instructors"`
	TAs           []string    `json:"tas"` // teaching assistants
}

// Talk is one presentation of a paper without workshop.
type Talk struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Speaker     string `json:"speaker"`
	Affiliation string `json:"affiliation"`
	Time        string `json:"time"`
	Room        string `json:"room"`
}

type AgendaRow struct {
	Time    string `json:"time"`
	Item    string `json:"item"`
	Speaker string `json:"speaker"`
}

type Registration struct {
	Intro         string    `json:"intro"`
	Currency      string    `json:"currency"`
	EarlyDeadline string    `json:"earlyDeadline"`
	Fees          []FeeRow  `json:"fees"`
	WorkshopFees  []FeeRow  `json:"workshopFees"`
	FeeNotes      []string  `json:"feeNotes"`
	Includes      []string  `json:"includes"`
	Policies      []Section `json:"policies"`
}

type FeeRow struct {
	Category string `json:"category"`
	Note     string `json:"note"`
	Early    string `json:"early"`
	Regular  string `json:"regular"`
	OnSite   string `json:"onSite"`
}

// Section is a generic titled block of paragraphs, bullet items and links.
type Section struct {
	Title      string   `json:"title"`
	Icon       string   `json:"icon"`
	Paragraphs []string `json:"paragraphs"`
	Items      []string `json:"items"`
	Links      []Link   `json:"links"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Sponsorship struct {
	Intro         string        `json:"intro"`
	Audience      []Stat        `json:"audience"`
	Packages      []Package     `json:"packages"`
	Benefits      []BenefitRow  `json:"benefits"`
	Opportunities []Card        `json:"opportunities"`
	Advantages    []Card        `json:"advantages"`
	Extras        []Section     `json:"extras"`
	Sponsors      []SponsorTier `json:"sponsors"`
	Partners      []Sponsor     `json:"partners"`
}

type Stat struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Icon  string `json:"icon"`
}

type Card struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Icon  string `json:"icon"`
}

type Package struct {
	Name    string `json:"name"`
	Price   string `json:"price"`
	GoodFor string `json:"goodFor"`
}

type BenefitRow struct {
	Group  string   `json:"group"` // starts a new group heading when it changes
	Label  string   `json:"label"`
	Values []string `json:"values"` // one per package: "yes", "no" or free text
}

type SponsorTier struct {
	Tier     string    `json:"tier"`
	Class    string    `json:"class"`
	Sponsors []Sponsor `json:"sponsors"`
	Slots    int       `json:"slots"` // placeholder tiles while sponsors are being confirmed
}

type Sponsor struct {
	Name string `json:"name"`
	Logo string `json:"logo"`
	URL  string `json:"url"`
}

type Journal struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

type FAQGroup struct {
	Title string `json:"title"`
	Items []FAQ  `json:"items"`
}

type FAQ struct {
	Q string `json:"q"`
	A string `json:"a"`
}

type Download struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"` // empty = not available yet
	Format      string `json:"format"`
	Icon        string `json:"icon"`
}

// DownloadGroup is one section of the Downloads page. Key is its anchor on
// that page and lets other pages show the group (e.g. the speaker kit).
type DownloadGroup struct {
	Key         string     `json:"key"`
	Title       string     `json:"title"`
	Icon        string     `json:"icon"`
	Description string     `json:"description"`
	Items       []Download `json:"items"`
}

type Edition struct {
	Year    string `json:"year"`
	Name    string `json:"name"`
	City    string `json:"city"`
	Dates   string `json:"dates"`
	URL     string `json:"url"`
	Note    string `json:"note"`
	Current bool   `json:"current"`
}

// Track returns the track with the given key.
func (s *Site) Track(key string) (Track, bool) {
	for _, t := range s.Tracks {
		if t.Key == key {
			return t, true
		}
	}
	return Track{}, false
}

// TotalSlots sums the speaking slots of all tracks.
func (s *Site) TotalSlots() int {
	n := 0
	for _, t := range s.Tracks {
		n += t.Slots
	}
	return n
}

// Load decodes and validates a Site from r.
func Load(r io.Reader) (*Site, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var s Site
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("content: decode site.json: %w", err)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// LoadFile reads a Site from a path on disk.
func LoadFile(path string) (*Site, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("content: %w", err)
	}
	defer f.Close()
	return Load(f)
}

// LoadFS reads a Site from name within fsys.
func LoadFS(fsys fs.FS, name string) (*Site, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("content: %w", err)
	}
	defer f.Close()
	return Load(f)
}

var validSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (s *Site) validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if strings.TrimSpace(s.Conference.Acronym) == "" {
		add("conference.acronym is required")
	}
	if strings.TrimSpace(s.Conference.Name) == "" {
		add("conference.name is required")
	}
	for _, key := range []string{TrackWithWorkshop, TrackWithoutWorkshop} {
		if _, ok := s.Track(key); !ok {
			add("tracks must include key %q", key)
		}
	}
	for i, w := range s.Workshops {
		if w.Capacity != "" && w.Capacity != "25" && w.Capacity != "50" {
			add("workshops[%d].capacity must be 25 or 50", i)
		}
	}
	if s.Conference.StartDate != "" {
		if _, ok := parseISO(s.Conference.StartDate); !ok {
			add("conference.startDate must be YYYY-MM-DD")
		}
	}
	for i, d := range s.ImportantDates {
		if d.ISO != "" {
			if _, ok := parseISO(d.ISO); !ok {
				add("importantDates[%d].iso must be YYYY-MM-DD", i)
			}
		}
	}
	calls := map[string]bool{}
	for i, c := range s.Calls {
		switch c.Status {
		case "open", "soon", "closed":
		default:
			add("calls[%d].status must be open, soon or closed", i)
		}
		if calls[c.Key] {
			add("calls[%d].key %q is used twice", i, c.Key)
		}
		calls[c.Key] = true
	}
	topics := map[string]bool{}
	for i, t := range s.Topics {
		if topics[t.Title] {
			add("topics[%d].title %q is used twice", i, t.Title)
		}
		topics[t.Title] = true
	}
	groups := map[string]bool{}
	for i, g := range s.Downloads {
		if !validSlug.MatchString(g.Key) {
			add("downloads[%d].key %q must be lowercase words joined by hyphens", i, g.Key)
		}
		if groups[g.Key] {
			add("downloads[%d].key %q is used twice", i, g.Key)
		}
		groups[g.Key] = true
	}
	for i, b := range s.Sponsorship.Benefits {
		if len(b.Values) != len(s.Sponsorship.Packages) {
			add("sponsorship.benefits[%d] (%s) has %d values for %d packages", i, b.Label, len(b.Values), len(s.Sponsorship.Packages))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("content: invalid site.json:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

func parseISO(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", v)
	return t, err == nil
}

func initials(name string) string {
	var out []rune
	for _, f := range strings.Fields(name) {
		r := []rune(f)
		if len(r) > 0 && len(out) < 2 {
			out = append(out, r[0])
		}
	}
	return strings.ToUpper(string(out))
}
