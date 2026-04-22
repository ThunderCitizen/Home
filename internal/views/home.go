package views

import (
	"thundercitizen/internal/council"
	"thundercitizen/templates/components"
)

// PendingMeeting is a hand-curated row rendered above the parsed recent
// meetings list on the home page. Used for meetings that are scheduled or
// have happened but don't yet have a PostMinutes PDF on eSCRIBE — once
// minutes post, the fetcher picks them up and the row is removed from here.
//
// Summary / KeyItems are distilled from the published agenda so readers get
// a preview even before minutes land. MinutesETA sets expectations about
// when the PostMinutes PDF is likely to appear (council approves the prior
// meeting's minutes at the next meeting, so minutes post ~a few days after
// that approval).
type PendingMeeting struct {
	Date       string   // human-readable, e.g. "Tuesday, April 21"
	Status     string   // "Upcoming" | "Pending minutes"
	AgendaURL  string   // eSCRIBE Agenda link (may be empty)
	Summary    string   // 1–2 sentence overview of the agenda
	KeyItems   []string // notable agenda items
	MinutesETA string   // when the PostMinutes PDF is expected, e.g. "Expected after the May 5 meeting"
}

// HomeViewModel contains data for the home page
type HomeViewModel struct {
	Hero            components.HeroProps
	QuickLinks      []components.LinkedCardProps
	RecentMeetings  []RecentMeetingView
	PendingMeetings []PendingMeeting
}

// PendingCouncilMeetings is the hand-curated list of upcoming / awaiting-minutes
// meetings, rendered above the parsed recent meetings on the home page.
// Remove entries once the fetcher ingests the PostMinutes PDF for each.
var PendingCouncilMeetings = []PendingMeeting{
	{
		Date:      "Tuesday, April 21",
		Status:    "Pending minutes",
		AgendaURL: "https://pub-thunderbay.escribemeetings.com/Meeting.aspx?Id=3c773247-1c29-4757-a367-0fe53fcce424&Agenda=Agenda&lang=English",
		Summary:   "Zoning changes, a tourism tax update, and a proposed 2.7% council pay raise.",
		KeyItems: []string{
			"Rezoning at 116-222 Coady Ave and 1240 Dawson Rd",
			"Tourism & Municipal Accommodation Tax update",
			"2026 Council Remuneration — 2.7% increase proposed",
			"$68K in external funding for poverty reduction & food security",
		},
		MinutesETA: "Expected after the May 5 council meeting",
	},
	{
		Date:      "Tuesday, April 7",
		Status:    "Pending minutes",
		AgendaURL: "https://pub-thunderbay.escribemeetings.com/Meeting.aspx?Id=2a8fc920-9a60-4ab1-99c0-e27ee2ef884f&Agenda=Agenda&lang=English",
		Summary:   "New fire chief, an emergency-management by-law overhaul, U-Pass renewal, and several surplus-land sales.",
		KeyItems: []string{
			"David Tarini appointed Chief of Fire",
			"New Emergency Management Program by-law (replaces the 2021 by-law)",
			"Lakehead University U-Pass transit agreement renewed",
			"Hammond Fire Training Centre $1.09M expansion (externally funded)",
			"Surplus land sales: 545 Algoma ($749K) and Fanshaw/Tokio/Arundel",
		},
		MinutesETA: "Expected this week — approved at the April 21 meeting",
	},
}

// RecentMeetingView is a compact meeting row for the home page.
type RecentMeetingView struct {
	Slug    string
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
			Slug:    council.MeetingSlug(m.Title, m.Date),
			ID:      m.ID,
			Date:    humanDate(m.Date),
			Summary: summary,
			Motions: m.MotionCount,
		}
	}

	return HomeViewModel{
		RecentMeetings:  recent,
		PendingMeetings: PendingCouncilMeetings,
		Hero: components.HeroProps{
			Title:    "Thunder Citizen",
			Lead:     "Data\u00a0for\u00a0the\u00a0People! (of\u00a0Thunder\u00a0Bay)",
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
