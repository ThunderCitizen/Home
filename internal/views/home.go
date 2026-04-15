package views

import (
	"thundercitizen/internal/council"
	"thundercitizen/templates/components"
)

// NextMeeting is the editorial card shown on the home page for the next
// council session. Hand-curated — set NextCouncilMeeting below to nil to
// hide the card when there's nothing to highlight.
type NextMeeting struct {
	Date      string   // human-readable, e.g. "Tuesday, April 21"
	Time      string   // e.g. "6:30 PM"
	Type      string   // e.g. "City Council"
	AgendaURL string   // eSCRIBE FileStream link; empty if not posted yet
	EventURL  string   // calendar.thunderbay.ca event detail URL
	Summary   string   // 1–2 sentences on what's notable
	KeyItems  []string // optional agenda highlights
}

// HomeViewModel contains data for the home page
type HomeViewModel struct {
	Hero           components.HeroProps
	QuickLinks     []components.LinkedCardProps
	RecentMeetings []RecentMeetingView
	NextMeeting    *NextMeeting
}

// NextCouncilMeeting is the single source of truth for the home page card.
// Update when the next meeting is announced; set to nil to hide the card.
var NextCouncilMeeting = &NextMeeting{
	Date:      "Tuesday, April 21",
	Time:      "6:30 PM",
	Type:      "City Council",
	AgendaURL: "", // agenda not yet posted on eSCRIBE
	EventURL:  "https://calendar.thunderbay.ca/default/Detail/2026-04-21-1830-City-Council",
	Summary:   "Regular council session. Agenda will be posted to eSCRIBE roughly one week before the meeting.",
}

// RecentMeetingView is a compact meeting row for the home page.
type RecentMeetingView struct {
	ID      string
	Date    string
	Summary string
	Motions int
}

// NewHomeViewModel creates the view model for the home page
func NewHomeViewModel(recentMeetings []council.MeetingSummary) HomeViewModel {
	recent := make([]RecentMeetingView, len(recentMeetings))
	for i, m := range recentMeetings {
		summary := m.Summary
		if len(summary) > 200 {
			cut := 200
			for cut > 150 && summary[cut] != ' ' {
				cut--
			}
			summary = summary[:cut] + "..."
		}
		recent[i] = RecentMeetingView{
			ID:      m.ID,
			Date:    humanDate(m.Date),
			Summary: summary,
			Motions: m.MotionCount,
		}
	}

	return HomeViewModel{
		RecentMeetings: recent,
		NextMeeting:    NextCouncilMeeting,
		Hero: components.HeroProps{
			Title:    "Thunder Citizen",
			Lead:     "Data for the People! (of Thunder Bay)",
			Subtitle: "",
		},
		QuickLinks: []components.LinkedCardProps{
			{
				Title:  "Budget",
				Href:   "/budget",
				Desc:   "Explore how your property taxes are allocated across city services.",
				Footer: "Budget visualizer",
			},
			{
				Title:  "Council",
				Href:   "/councillors",
				Desc:   "Browse voting records, key quotes, and decision-making patterns.",
				Footer: "Profiles · Voting records",
			},
			{
				Title:  "Transit",
				Href:   "/transit",
				Desc:   "Live bus tracking, service trends, and route finder.",
				Footer: "Live map · Metrics",
			},
		},
	}
}
