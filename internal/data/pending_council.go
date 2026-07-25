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
var PendingCouncilMeetings = []PendingMeeting{}
