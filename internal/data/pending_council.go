package data

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
	DateISO    string   // sortable date, YYYY-MM-DD
	Date       string   // human-readable, e.g. "Tuesday, April 21"
	Status     string   // "Upcoming" | "Pending minutes"
	AgendaURL  string   // eSCRIBE Agenda link (may be empty)
	Summary    string   // 1–2 sentence overview of the agenda
	KeyItems   []string // notable agenda items
	MinutesETA string   // when the PostMinutes PDF is expected, e.g. "Expected after the May 5 meeting"
}

// PendingCouncilMeetings is the hand-curated list of upcoming / awaiting-minutes
// meetings, rendered above the parsed recent meetings on the home page.
// Remove entries once the fetcher ingests the PostMinutes PDF for each.
var PendingCouncilMeetings = []PendingMeeting{
	{
		DateISO:   "2026-06-02",
		Date:      "Tuesday, June 2, 2026",
		Status:    "Pending minutes",
		AgendaURL: "https://pub-thunderbay.escribemeetings.com/Meeting.aspx?Id=51c8071e-5815-4a20-b831-418f4439c55e&Agenda=Agenda&lang=English",
		Summary:   "Still awaiting the official PostMinutes PDF for June 2. The agenda included a NOHFC funding bid for the Canada Games Complex, Microsoft 365 licensing, a centralized customer service update, and an Indigenous data governance presentation.",
		KeyItems: []string{
			"Canada Games Complex capital enhancements — Stage 2 NOHFC funding application for sport-event hosting (Report 251-2026)",
			"Microsoft 365 licensing & digitization — single-source procurement (Report 188-2026)",
			"Centralized Customer Service update (Report 217-2026)",
			"Indigenous Data Governance presentation",
			"Holding Symbol removal at 2019 Almira Avenue (By-law 259-2026)",
		},
		MinutesETA: "Not yet posted on eSCRIBE; June 23 is processed",
	},
}
