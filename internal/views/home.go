package views

import (
	"sort"
	"strings"

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
	CouncilActivity []CouncilActivityView
}

// RecentMeetingView is a compact meeting row for the home page.
type RecentMeetingView struct {
	Slug     string
	ID       string
	DateISO  string
	Date     string
	Summary  string
	Motions  int
	KeyItems []string
}

// CouncilActivityView is one calendar-sorted row in the home page's
// Recent Council Activity card. Rows may be parsed meetings or pending gaps.
type CouncilActivityView struct {
	Pending    bool
	SortDate   string
	Slug       string
	Date       string
	Summary    string
	Motions    int
	KeyItems   []string
	MinutesETA string
}

// shortSummary trims a meeting summary down to roughly limit characters for the
// compact home card, but only ever on a sentence boundary — never mid-sentence,
// and with no trailing ellipsis. If the first sentence already exceeds the
// limit it's returned whole; a summary with no sentence punctuation is returned
// as-is rather than cut.
func shortSummary(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	end := 0
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '.' || c == '!' || c == '?' {
			if i+1 >= len(s) || s[i+1] == ' ' {
				end = i + 1
				if end >= limit {
					break
				}
			}
		}
	}
	if end == 0 {
		return s
	}
	return s[:end]
}

// NewHomeViewModel creates the view model for the home page
func NewHomeViewModel(recentMeetings []council.MeetingSummary) HomeViewModel {
	recent := make([]RecentMeetingView, len(recentMeetings))
	for i, m := range recentMeetings {
		recent[i] = RecentMeetingView{
			Slug:     council.MeetingSlug(m.Title, m.Date),
			ID:       m.ID,
			DateISO:  m.Date,
			Date:     humanDate(m.Date),
			Summary:  shortSummary(m.Summary, 200),
			Motions:  m.MotionCount,
			KeyItems: m.KeyItems,
		}
	}
	activity := buildCouncilActivity(recent, data.PendingCouncilMeetings)

	return HomeViewModel{
		RecentMeetings:  recent,
		PendingMeetings: data.PendingCouncilMeetings,
		CouncilActivity: activity,
		Hero: components.HeroProps{
			Title:    "Thunder Citizen",
			Lead:     "Thunder\u00a0Bay's public data, in one\u00a0place.",
			Subtitle: "Transit, council, and the budget — made clear.",
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
				Desc:   "Thunder Bay's 13 city councillors, their voting records, and ward boundaries.",
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

func buildCouncilActivity(recent []RecentMeetingView, pending []data.PendingMeeting) []CouncilActivityView {
	activity := make([]CouncilActivityView, 0, len(recent)+len(pending))
	for _, m := range recent {
		activity = append(activity, CouncilActivityView{
			SortDate: m.DateISO,
			Slug:     m.Slug,
			Date:     m.Date,
			Summary:  m.Summary,
			Motions:  m.Motions,
			KeyItems: m.KeyItems,
		})
	}
	for _, m := range pending {
		activity = append(activity, CouncilActivityView{
			Pending:    true,
			SortDate:   m.DateISO,
			Date:       m.Date,
			Summary:    m.Summary,
			KeyItems:   m.KeyItems,
			MinutesETA: m.MinutesETA,
		})
	}
	sort.SliceStable(activity, func(i, j int) bool {
		return activity[i].SortDate > activity[j].SortDate
	})
	return activity
}
