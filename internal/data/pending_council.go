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
