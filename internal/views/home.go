package views

import (
	"thundercitizen/internal/council"
	"thundercitizen/internal/data"
	"thundercitizen/templates/components"
)

// HomeViewModel contains data for the home page
type HomeViewModel struct {
	Hero            components.HeroProps
	QuickLinks      []components.LinkedCardProps
	RecentMeetings  []RecentMeetingView
	PendingMeetings []data.PendingMeeting
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
		PendingMeetings: data.PendingCouncilMeetings,
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
