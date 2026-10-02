package server

import (
	"strings"

	"icas/internal/content"
)

// Page describes one URL of the site.
type Page struct {
	Path        string
	Template    string // file name in templates/pages without .html
	Title       string
	Lead        string // subtitle shown in the page banner
	Description string // meta description; Lead is used when empty
	Parent      string // path of the parent page, for breadcrumbs
	Sidebar     string // which section menu the sidebar shows
	Key         string // page-specific lookup key (event slug)
	Home        bool
	NoIndex     bool // keep out of sitemap.xml and search engines
	Private     bool // never cache (admin)
}

// MetaDescription prefers the explicit description, then the banner lead.
func (p *Page) MetaDescription() string {
	if p.Description != "" {
		return p.Description
	}
	return p.Lead
}

var notFoundPage = &Page{Path: "/404", Template: "not-found", Title: "Page Not Found", Lead: "The page you are looking for does not exist or has moved.", NoIndex: true}

const eventsPath = "/program/special-events"

func eventPath(slug string) string { return "/program/events/" + slug }

func sitePages(site *content.Site) []*Page {
	pages := []*Page{
		{Path: "/", Template: "home", Home: true},

		// About
		{Path: "/about", Template: "about", Title: "About ICAS", Lead: "A forum for the science and engineering of systems that sense, decide and act on their own.", Parent: "/", Sidebar: "about"},
		{Path: "/about/welcome", Template: "welcome", Title: "Welcome Message", Lead: "A word from the organizing committee.", Parent: "/about", Sidebar: "about"},
		{Path: "/news", Template: "news", Title: "News Highlights", Lead: "Announcements, deadline changes and program updates.", Parent: "/about", Sidebar: "about"},
		{Path: "/committee", Template: "committee", Title: "Organizing Committee", Lead: "The people organizing the conference.", Parent: "/about", Sidebar: "about"},
		{Path: "/committee/technical-program", Template: "tpc", Title: "Technical Program Committee", Lead: "Track chairs and reviewers responsible for the double-blind review.", Parent: "/committee", Sidebar: "about"},
		{Path: "/about/history", Template: "history", Title: "Conference History", Lead: "Past and upcoming editions of ICAS.", Parent: "/about", Sidebar: "about"},
		{Path: "/promotional-items", Template: "promotional", Title: "Promotional Items", Lead: "Logos, social media kits, flyers and slides to help spread the word.", Parent: "/about", Sidebar: "about"},
		{Path: "/faq", Template: "faq", Title: "Frequently Asked Questions", Lead: "Quick answers about submissions, workshops, registration and travel.", Parent: "/about", Sidebar: "about"},
		{Path: "/policies", Template: "policies", Title: "Conference Policies", Lead: "Code of conduct, privacy, non-discrimination, accessibility, ethics and terms of use.", Parent: "/about", Sidebar: "about"},

		// Call for papers
		{Path: "/call-for-papers", Template: "cfp", Title: "Call for Papers", Lead: "Papers with and without workshop on autonomous systems — 13 speaking slots.", Parent: "/", Sidebar: "cfp"},
		{Path: "/call-for-papers/with-workshop", Template: "cfp-with-workshop", Title: "Papers with Workshop", Lead: "A 25-minute morning presentation and a 90-minute afternoon workshop for 25 or 50 participants.", Parent: "/call-for-papers", Sidebar: "cfp"},
		{Path: "/call-for-papers/without-workshop", Template: "cfp-without-workshop", Title: "Papers without Workshop", Lead: "Presentations of about 15 minutes on academic work relevant to autonomous systems.", Parent: "/call-for-papers", Sidebar: "cfp"},
		{Path: "/call-for-papers/topics", Template: "cfp-topics", Title: "Conference Theme & Topics", Lead: "Sixteen topic areas, led by the implementation topics at the heart of ICAS.", Parent: "/call-for-papers", Sidebar: "cfp"},
		{Path: "/call-for-papers/requirements", Template: "cfp-requirements", Title: "Proposal Submission Requirements", Lead: "What every proposal must contain, by track.", Parent: "/call-for-papers", Sidebar: "cfp"},
		{Path: "/call-for-papers/review", Template: "cfp-review", Title: "Review Criteria", Lead: "How proposals are evaluated in double-blind peer review.", Parent: "/call-for-papers", Sidebar: "cfp"},
		{Path: "/call-for-papers/special-sessions", Template: "cfp-special-sessions", Title: "Call for Special Sessions", Lead: "Propose a focused session on an emerging or interdisciplinary topic in autonomy.", Parent: "/call-for-papers", Sidebar: "cfp"},
		{Path: "/call-for-papers/cross-community", Template: "cfp-cross-community", Title: "Call for Cross-Community Special Sessions", Lead: "Special sessions co-organized with neighbouring research communities.", Parent: "/call-for-papers/special-sessions", Sidebar: "cfp"},
		{Path: "/call-for-papers/tutorials", Template: "cfp-tutorials", Title: "Call for Tutorials", Lead: "Share in-depth, practical knowledge with the autonomy community.", Parent: "/call-for-papers", Sidebar: "cfp"},
		{Path: "/call-for-papers/demonstrations", Template: "cfp-demos", Title: "Call for Live Demonstrations", Lead: "Show working autonomous systems — robots, vehicles, software — on site.", Parent: "/call-for-papers", Sidebar: "cfp"},
		{Path: "/call-for-papers/challenge", Template: "cfp-challenge", Title: "Autonomy Grand Challenge", Lead: "A competition track for teams building autonomous systems.", Parent: "/call-for-papers", Sidebar: "cfp"},

		// Authors
		{Path: "/submission", Template: "submission", Title: "Proposal Submission", Lead: "Submit a paper with workshop or a paper without workshop online.", Parent: "/", Sidebar: "authors"},
		{Path: "/submission/received", Template: "submission-received", Title: "Proposal Received", Lead: "Thank you for submitting to ICAS.", Parent: "/submission", NoIndex: true},
		{Path: "/important-dates", Template: "dates", Title: "Important Dates", Lead: "Deadlines for proposals, notifications, registration and the conference itself.", Parent: "/", Sidebar: "authors"},
		{Path: "/authors", Template: "authors", Title: "Author Information", Lead: "Everything authors and speakers need to know, from submission to presentation day.", Parent: "/", Sidebar: "authors"},
		{Path: "/authors/presentation-guidelines", Template: "presentation", Title: "Presentation Guidelines", Lead: "Timing, equipment and preparation for papers with and without workshop, and for live demonstrations.", Parent: "/authors", Sidebar: "authors"},
		{Path: "/authors/publication", Template: "publication", Title: "Publication Opportunities", Lead: "Proceedings and journal special issue for accepted work.", Parent: "/authors", Sidebar: "authors"},
		{Path: "/authors/travel-grants", Template: "travel-grants", Title: "Student Travel Grants", Lead: "Support for students presenting at ICAS.", Parent: "/authors", Sidebar: "authors"},

		// Program
		{Path: "/program", Template: "program", Title: "Program at a Glance", Lead: "Morning presentations of papers with workshop; in the afternoon, workshops run in parallel with the paper sessions.", Parent: "/", Sidebar: "program"},
		{Path: "/program/keynotes", Template: "keynotes", Title: "Keynote Speakers", Lead: "Plenary talks by leaders in autonomous systems.", Parent: "/program", Sidebar: "program"},
		{Path: "/program/workshops", Template: "workshops", Title: "Workshops", Lead: "Five 90-minute, laptop-based workshops for 25 or 50 participants, led by the speakers of papers with workshop.", Parent: "/program", Sidebar: "program"},
		{Path: "/program/sessions", Template: "sessions", Title: "Paper Sessions", Lead: "Eight papers without workshop, about 15 minutes each, presented in parallel with the afternoon workshops.", Parent: "/program", Sidebar: "program"},
		{Path: "/program/tutorials", Template: "tutorials", Title: "Tutorials", Lead: "In-depth tutorials on core techniques for autonomy.", Parent: "/program", Sidebar: "program"},
		{Path: eventsPath, Template: "special-events", Title: "Special Events & Forums", Lead: "Industry forum, student competition, mentoring and community events.", Parent: "/program", Sidebar: "program"},
		{Path: "/program/social-events", Template: "social-events", Title: "Social Events", Lead: "Receptions and networking around the technical program.", Parent: "/program", Sidebar: "program"},
		{Path: "/program/downloads", Template: "program-downloads", Title: "Program Book & Proceedings", Lead: "Downloadable program, proceedings and the conference web app.", Parent: "/program", Sidebar: "program"},

		// Registration
		{Path: "/registration", Template: "registration", Title: "Registration Information", Lead: "Fees, what is included, and registration policies.", Parent: "/", Sidebar: "registration"},
		{Path: "/registration/workshops", Template: "registration-workshops", Title: "Workshop Registration", Lead: "Each workshop has 25 or 50 seats, booked separately from the conference registration.", Parent: "/registration", Sidebar: "registration"},

		// Sponsors & exhibitors
		{Path: "/sponsors", Template: "sponsors", Title: "Become a Sponsor or Exhibitor", Lead: "Meet the researchers and engineers building autonomous systems.", Parent: "/", Sidebar: "sponsors"},
		{Path: "/sponsors/list", Template: "sponsor-list", Title: "Our Sponsors", Lead: "We thank our sponsors and exhibitors for their generous support.", Parent: "/sponsors", Sidebar: "sponsors"},

		// Travel
		{Path: "/venue", Template: "venue", Title: "Conference Venue", Lead: "Where ICAS takes place and how to reach it.", Parent: "/", Sidebar: "travel"},
		{Path: "/travel/city", Template: "city", Title: "Host City", Lead: "Discover the city hosting ICAS.", Parent: "/venue", Sidebar: "travel"},
		{Path: "/travel/hotels", Template: "hotels", Title: "Hotel Recommendation", Lead: "Accommodation near the venue.", Parent: "/venue", Sidebar: "travel"},
		{Path: "/travel/getting-there", Template: "getting-there", Title: "Getting There", Lead: "Arriving by plane, train, bus or car, and getting around locally.", Parent: "/venue", Sidebar: "travel"},
		{Path: "/travel/visa", Template: "visa", Title: "Visa Information", Lead: "Entry requirements and invitation letters for international participants.", Parent: "/venue", Sidebar: "travel"},

		// Contact & utilities
		{Path: "/contact", Template: "contact", Title: "Contact Us", Lead: "Questions about submissions, registration or sponsorship? We are happy to help.", Parent: "/"},
		{Path: "/subscribe", Template: "subscribe", Title: "Keep Me Updated", Lead: "Deadline reminders and announcements, straight to your inbox.", Parent: "/"},
		{Path: "/sitemap", Template: "sitemap", Title: "Site Map", Lead: "Every page of the ICAS website.", Parent: "/"},
		{Path: "/admin", Template: "admin", Title: "Admin", Lead: "Submissions, contact messages and subscribers.", Parent: "/", NoIndex: true, Private: true},
	}
	for _, e := range site.Events {
		pages = append(pages, &Page{Path: eventPath(e.Slug), Template: "event", Title: e.Title, Lead: e.Summary,
			Parent: eventsPath, Sidebar: "program", Key: e.Slug})
	}
	return pages
}

// navItem is a node of the main menu. Children of children render as a
// fly-out sub-menu.
type navItem struct {
	Label    string
	Path     string
	Note     string // small badge text
	Divider  bool   // draw a separator above this entry
	Children []navItem
}

func siteNav(site *content.Site) []navItem {
	var events []navItem
	for _, e := range site.Events {
		label := e.NavLabel
		if label == "" {
			label = e.Title
		}
		events = append(events, navItem{Label: label, Path: eventPath(e.Slug)})
	}
	return []navItem{
		{Label: "Home", Path: "/"},
		{Label: "About", Path: "/about", Children: []navItem{
			{Label: "Welcome Message", Path: "/about/welcome"},
			{Label: "About ICAS", Path: "/about"},
			{Label: "News Highlights", Path: "/news"},
			{Label: "Organizing Committee", Path: "/committee"},
			{Label: "Technical Program Committee", Path: "/committee/technical-program"},
			{Label: "Conference History", Path: "/about/history"},
			{Label: "Promotional Items", Path: "/promotional-items"},
			{Label: "FAQs", Path: "/faq"},
			{Label: "Conference Policies", Path: "/policies"},
		}},
		{Label: "Call for Papers", Path: "/call-for-papers", Children: []navItem{
			{Label: "Call for Papers", Path: "/call-for-papers"},
			{Label: "Papers with Workshop", Path: "/call-for-papers/with-workshop"},
			{Label: "Papers without Workshop", Path: "/call-for-papers/without-workshop"},
			{Label: "Conference Theme & Topics", Path: "/call-for-papers/topics"},
			{Label: "Submission Requirements", Path: "/call-for-papers/requirements"},
			{Label: "Review Criteria", Path: "/call-for-papers/review"},
			{Label: "Call for Special Sessions", Path: "/call-for-papers/special-sessions", Divider: true, Children: []navItem{
				{Label: "Cross-Community Special Sessions", Path: "/call-for-papers/cross-community"},
			}},
			{Label: "Call for Tutorials", Path: "/call-for-papers/tutorials"},
			{Label: "Call for Live Demonstrations", Path: "/call-for-papers/demonstrations"},
			{Label: "Autonomy Grand Challenge", Path: "/call-for-papers/challenge"},
			{Label: "Exhibition & Sponsorship Prospectus", Path: "/sponsors", Divider: true},
		}},
		{Label: "Authors", Path: "/authors", Children: []navItem{
			{Label: "Proposal Submission", Path: "/submission"},
			{Label: "Important Dates", Path: "/important-dates"},
			{Label: "Author Information", Path: "/authors"},
			{Label: "Presentation Guidelines", Path: "/authors/presentation-guidelines"},
			{Label: "Publication Opportunities", Path: "/authors/publication"},
			{Label: "Student Travel Grants", Path: "/authors/travel-grants"},
		}},
		{Label: "Program", Path: "/program", Children: []navItem{
			{Label: "Program at a Glance", Path: "/program"},
			{Label: "Keynote Speakers", Path: "/program/keynotes"},
			{Label: "Workshops", Path: "/program/workshops"},
			{Label: "Paper Sessions", Path: "/program/sessions"},
			{Label: "Tutorials", Path: "/program/tutorials"},
			{Label: "Special Events & Forums", Path: eventsPath, Children: events},
			{Label: "Social Events", Path: "/program/social-events"},
			{Label: "Program Book & Proceedings", Path: "/program/downloads"},
		}},
		{Label: "Registration", Path: "/registration", Children: []navItem{
			{Label: "Registration Information", Path: "/registration"},
			{Label: "Workshop Registration", Path: "/registration/workshops"},
			{Label: "Visa Invitation Letter", Path: "/travel/visa"},
		}},
		{Label: "Sponsors & Exhibitors", Path: "/sponsors", Children: []navItem{
			{Label: "Become a Sponsor / Exhibitor", Path: "/sponsors"},
			{Label: "Sponsor List", Path: "/sponsors/list"},
		}},
		{Label: "Travel", Path: "/venue", Children: []navItem{
			{Label: "Conference Venue", Path: "/venue"},
			{Label: "Host City", Path: "/travel/city"},
			{Label: "Hotel Recommendation", Path: "/travel/hotels"},
			{Label: "Getting There", Path: "/travel/getting-there"},
			{Label: "Visa Information", Path: "/travel/visa"},
		}},
		{Label: "Contact", Path: "/contact"},
	}
}

// within reports whether page p is path or a descendant of it in the page
// tree (following Parent links).
func (s *Server) within(p *Page, path string) bool {
	for cur, guard := p, 0; cur != nil && guard < 10; cur, guard = s.byPath[cur.Parent], guard+1 {
		if cur.Path == path {
			return true
		}
	}
	return false
}

func (s *Server) viewOf(item navItem, page *Page) navView {
	v := navView{Label: item.Label, Path: item.Path, Note: item.Note, Divider: item.Divider,
		External: strings.HasPrefix(item.Path, "http"), Active: item.Path == page.Path}
	for _, c := range item.Children {
		cv := s.viewOf(c, page)
		if cv.Active {
			v.Active = true
		}
		v.Children = append(v.Children, cv)
	}
	return v
}

func (s *Server) navFor(page *Page) []navView {
	out := make([]navView, 0, len(s.nav))
	for _, item := range s.nav {
		v := s.viewOf(item, page)
		if item.Path == "/" {
			v.Active = page.Home
		} else if !v.Active {
			v.Active = s.within(page, item.Path)
		}
		out = append(out, v)
	}
	// A page may be linked from several menus (the visa page, the sponsor
	// prospectus); highlight only the menu its breadcrumb trail belongs to.
	active := 0
	for _, v := range out {
		if v.Active {
			active++
		}
	}
	if active > 1 {
		for i := range out {
			out[i].Active = out[i].Path != "/" && s.within(page, out[i].Path)
		}
	}
	return out
}

func (s *Server) crumbsFor(page *Page) []crumb {
	if page.Home {
		return nil
	}
	var trail []crumb
	for cur, guard := s.byPath[page.Parent], 0; cur != nil && guard < 10; cur, guard = s.byPath[cur.Parent], guard+1 {
		label := cur.Title
		if cur.Home {
			label = "Home"
		}
		trail = append([]crumb{{Label: label, Path: cur.Path}}, trail...)
		if cur.Home {
			break
		}
	}
	if len(trail) == 0 || trail[0].Path != "/" {
		trail = append([]crumb{{Label: "Home", Path: "/"}}, trail...)
	}
	return append(trail, crumb{Label: page.Title})
}

// sidebarSections maps a page's Sidebar key to the menu whose links it lists.
var sidebarSections = map[string]string{
	"about": "/about", "cfp": "/call-for-papers", "authors": "/authors", "program": "/program",
	"registration": "/registration", "sponsors": "/sponsors", "travel": "/venue",
}

// sidebarMenu returns the section menu shown in a page's sidebar.
func (s *Server) sidebarMenu(page *Page) *navView {
	want, ok := sidebarSections[page.Sidebar]
	if !ok {
		return nil
	}
	for _, item := range s.nav {
		if item.Path == want {
			v := s.viewOf(item, page)
			return &v
		}
	}
	return nil
}
