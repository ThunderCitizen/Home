package views

import (
	"fmt"
	"math"
	"strings"
	"time"

	"thundercitizen/internal/council"
	"thundercitizen/templates/components"
)

// CouncilViewModel is the view model for the /council meeting list page.
type CouncilViewModel struct {
	Meetings     []MeetingSummaryView
	Stats        CouncilStatsView
	Filter       CouncilFilterView
	Pagination   PaginationView
	MotionSearch *MotionSearchViewModel // non-nil when ?q= search is active
}

// MeetingViewModel is the view model for a single meeting page.
type MeetingViewModel struct {
	ID               string
	Date             string
	Weekday          string
	Title            string
	Term             string
	TermLabel        string
	MinutesURL       string
	Summary          string // LLM meeting summary (same used on index page)
	Motions          []MotionView
	MotionCount      int
	RecordedVotes    int
	SubstantiveCount int
	ProceduralCount  int
	CarriedCount     int // substantive motions with result CARRIED
	LostCount        int // substantive motions with result LOST
	NotableCount     int // motions that are headline/notable OR have media coverage
}

// MeetingSummaryView is a meeting row for the list page.
type MeetingSummaryView struct {
	Slug            string
	ID              string
	Date            string
	Term            string
	MinutesURL      string
	Summary         string
	SummaryPreview  string // first item only, for table display
	MotionCount     int
	RecordedVotes   int
	CarriedCount    int
	LostCount       int
	HeadlineCount   int
	NotableCount    int
	RoutineCount    int
	ProceduralCount int
	MediaCount      int
}

// MotionView is a presentation-ready motion within a meeting.
type MotionView struct {
	ID           int64
	AgendaItem   string
	Heading      string // AgendaItem, else llm_label — what the template should display
	Summary      string
	MovedBy      string
	SecondedBy   string
	Text         string
	TextPreview  string
	TextClauses  []string // split "AND THAT" clauses for readability
	IsProcedural bool     // agenda confirmation, minutes adoption, by-laws
	Result       string
	ResultClass  string
	Significance string
	MediaURL     string
	HasVote      bool
	VoteSummary  string
	YeaCount     int
	NayCount     int
	Votes        *council.VoteRecord
	Roster       *components.VoteRosterProps // populated for meeting page; nil for search/list
}

// CouncilStatsView holds aggregate stats.
type CouncilStatsView struct {
	TotalMeetings string
	TotalMotions  string
	RecordedVotes string
}

// CouncilFilterView holds current filter state.
type CouncilFilterView struct {
	Query         string
	Term          string
	TermYear      int // for YearSelector (2022 or 2018)
	RecordedVotes bool
	Headline      bool
	Notable       bool
}

// PaginationView holds pagination state.
type PaginationView struct {
	Page       int
	TotalPages int
	Total      int
	HasPrev    bool
	HasNext    bool
	PrevPage   int
	NextPage   int
}

// NewCouncilViewModel builds the meeting list view model.
func NewCouncilViewModel(meetings []council.MeetingSummary, total int, stats [3]int, filter council.MeetingFilter) CouncilViewModel {
	views := make([]MeetingSummaryView, len(meetings))
	for i, m := range meetings {
		preview := m.Summary
		if idx := strings.Index(preview, "; "); idx > 0 {
			preview = preview[:idx]
		}
		if len(preview) > 120 {
			cut := 120
			for cut > 80 && preview[cut] != ' ' {
				cut--
			}
			preview = preview[:cut] + "..."
		}

		views[i] = MeetingSummaryView{
			Slug:            council.MeetingSlug(m.Title, m.Date),
			ID:              m.ID,
			Date:            humanDate(m.Date),
			Term:            m.Term,
			MinutesURL:      m.MinutesURL,
			Summary:         m.Summary,
			SummaryPreview:  preview,
			MotionCount:     m.MotionCount,
			RecordedVotes:   m.RecordedVotes,
			CarriedCount:    m.CarriedCount,
			LostCount:       m.LostCount,
			HeadlineCount:   m.HeadlineCount,
			NotableCount:    m.NotableCount,
			RoutineCount:    m.RoutineCount,
			ProceduralCount: m.ProceduralCount,
			MediaCount:      m.MediaCount,
		}
	}

	page := filter.Offset/filter.Limit + 1
	totalPages := int(math.Ceil(float64(total) / float64(filter.Limit)))
	if totalPages < 1 {
		totalPages = 1
	}

	return CouncilViewModel{
		Meetings: views,
		Stats: CouncilStatsView{
			TotalMeetings: fmt.Sprintf("%d", stats[0]),
			TotalMotions:  fmt.Sprintf("%d", stats[1]),
			RecordedVotes: fmt.Sprintf("%d", stats[2]),
		},
		Filter: CouncilFilterView{
			Query:         filter.Query,
			Term:          filter.Term,
			TermYear:      termYear(filter.Term),
			RecordedVotes: filter.RecordedVotes,
			Headline:      filter.Headline,
			Notable:       filter.Notable,
		},
		Pagination: PaginationView{
			Page:       page,
			TotalPages: totalPages,
			Total:      total,
			HasPrev:    page > 1,
			HasNext:    page < totalPages,
			PrevPage:   page - 1,
			NextPage:   page + 1,
		},
	}
}

// NewMeetingViewModel builds the single meeting view model.
func NewMeetingViewModel(md *council.MeetingDetail) MeetingViewModel {
	photos := PhotoByName()
	motions := make([]MotionView, len(md.Motions))
	for i, m := range md.Motions {
		motions[i] = motionRowToView(m)
		motions[i].Roster = BuildVoteRoster(motions[i].Votes, motions[i].Result, photos)
	}

	// Counts span all motions (substantive + procedural) so the filter pills
	// add up consistently against the "All" total.
	var recorded, substantive, procedural, carried, lost int
	var notable int
	for _, m := range motions {
		if m.HasVote {
			recorded++
		}
		if m.MediaURL != "" {
			notable++
		}
		if m.IsProcedural {
			procedural++
		} else {
			substantive++
		}
		switch m.Result {
		case "CARRIED":
			carried++
		case "LOST":
			lost++
		}
	}

	weekday := ""
	if t, err := time.Parse("2006-01-02", md.Date); err == nil {
		weekday = t.Format("Monday")
	}

	return MeetingViewModel{
		ID:               md.ID,
		Date:             humanDate(md.Date),
		Weekday:          weekday,
		Title:            md.Title,
		Term:             md.Term,
		TermLabel:        humanTerm(md.Term),
		MinutesURL:       md.MinutesURL,
		Summary:          md.Summary,
		Motions:          motions,
		MotionCount:      len(motions),
		RecordedVotes:    recorded,
		SubstantiveCount: substantive,
		ProceduralCount:  procedural,
		CarriedCount:     carried,
		LostCount:        lost,
		NotableCount:     notable,
	}
}

func termYear(term string) int {
	if len(term) >= 4 {
		y := 0
		for _, c := range term[:4] {
			y = y*10 + int(c-'0')
		}
		return y
	}
	return 2022 // default
}

// humanDate formats "2025-04-28" as "April 28, 2025".
func humanDate(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("January 2, 2006")
}

// humanTerm formats "2022-2026" as "2022–2026 Term".
func humanTerm(term string) string {
	if len(term) == 9 && term[4] == '-' {
		return term[:4] + "–" + term[5:] + " Term"
	}
	return term
}

// splitClauses breaks motion text into the lead clause and "AND THAT" subclauses.
func splitClauses(text string) []string {
	if text == "" {
		return nil
	}
	// Split on "; AND THAT" or " AND THAT" (common council motion patterns)
	parts := strings.Split(text, "; AND THAT ")
	if len(parts) == 1 {
		parts = strings.Split(text, " AND THAT ")
	}
	if len(parts) <= 1 {
		return []string{text}
	}
	clauses := make([]string, len(parts))
	clauses[0] = strings.TrimRight(parts[0], ";")
	for i := 1; i < len(parts); i++ {
		clauses[i] = parts[i]
	}
	return clauses
}

// isProcedural checks if a motion is procedural boilerplate.
func isProcedural(text string) bool {
	lower := strings.ToLower(text)
	procedural := []string{
		"that the minutes of the following",
		"that the agenda as printed",
		"that the following by-law be introduced",
		"that the following by-laws be introduced",
		"that the following resolution be introduced",
		"that the hour being",
		"that city council recess",
		"that the consent agenda",
	}
	for _, p := range procedural {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// MotionSearchViewModel is the view model for the /motions search page.
type MotionSearchViewModel struct {
	Motions       []MotionSearchRow
	Filter        MotionSearchFilter
	Pagination    PaginationView
	RecordedVotes int // count of motions with recorded votes in this result set
}

// MotionSearchRow is a motion result with meeting context.
type MotionSearchRow struct {
	MotionView
	Date        string
	MeetingID   string
	MeetingSlug string
}

// MotionSearchFilter holds current search/filter state.
type MotionSearchFilter struct {
	Query        string
	Term         string
	TermYear     int
	Significance string
	Result       string
}

// NewMotionSearchViewModel builds the motion search view model.
func NewMotionSearchViewModel(motions []council.MotionRow, total int, filter council.MotionFilter) MotionSearchViewModel {
	rows := make([]MotionSearchRow, len(motions))
	recorded := 0
	for i, m := range motions {
		mv := motionRowToView(m)
		rows[i] = MotionSearchRow{
			MotionView:  mv,
			Date:        humanDate(m.Date),
			MeetingID:   m.MeetingID,
			MeetingSlug: council.MeetingSlug("City Council", m.Date),
		}
		if mv.HasVote {
			recorded++
		}
	}

	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	page := filter.Offset/filter.Limit + 1
	totalPages := int(math.Ceil(float64(total) / float64(filter.Limit)))
	if totalPages < 1 {
		totalPages = 1
	}

	return MotionSearchViewModel{
		Motions: rows,
		Filter: MotionSearchFilter{
			Query:        filter.Query,
			Term:         filter.Term,
			TermYear:     termYear(filter.Term),
			Significance: filter.Significance,
			Result:       filter.Result,
		},
		Pagination: PaginationView{
			Page:       page,
			TotalPages: totalPages,
			Total:      total,
			HasPrev:    page > 1,
			HasNext:    page < totalPages,
			PrevPage:   page - 1,
			NextPage:   page + 1,
		},
		RecordedVotes: recorded,
	}
}

func motionRowToView(m council.MotionRow) MotionView {
	heading := m.AgendaItem
	if heading == "" {
		heading = m.Label
	}
	mv := MotionView{
		ID:           m.ID,
		AgendaItem:   m.AgendaItem,
		Heading:      heading,
		Summary:      m.Summary,
		MovedBy:      m.MovedBy,
		SecondedBy:   m.SecondedBy,
		Text:         m.Text,
		Result:       m.Result,
		Significance: m.Significance,
		YeaCount:     m.YeaCount,
		NayCount:     m.NayCount,
		HasVote:      m.YeaCount > 0 || m.NayCount > 0,
	}

	if m.MediaURL != nil {
		mv.MediaURL = *m.MediaURL
	}

	switch m.Result {
	case "CARRIED":
		mv.ResultClass = "carried"
	case "LOST":
		mv.ResultClass = "lost"
	case "TIE":
		mv.ResultClass = "tie"
	}

	if mv.HasVote {
		mv.VoteSummary = fmt.Sprintf("%d-%d", m.YeaCount, m.NayCount)
	}

	mv.Votes = m.Votes
	mv.TextClauses = splitClauses(m.Text)
	mv.IsProcedural = isProcedural(m.Text)

	// Truncate for list view
	mv.TextPreview = m.Text
	if len(mv.TextPreview) > 250 {
		cut := 250
		for cut > 200 && mv.TextPreview[cut] != ' ' {
			cut--
		}
		mv.TextPreview = mv.TextPreview[:cut] + "..."
	}

	return mv
}

// BuildVoteRoster enriches a VoteRecord with photos and tally formatting.
// Returns nil when there's nothing to show (no record, or no votes on either side).
// `photos` should come from PhotoByName(); pass nil to render initials-only avatars.
func BuildVoteRoster(rec *council.VoteRecord, result string, photos map[string]string) *components.VoteRosterProps {
	if rec == nil {
		return nil
	}
	yea := len(rec.For)
	nay := len(rec.Against)
	if yea == 0 && nay == 0 {
		return nil
	}

	v := &components.VoteRosterProps{
		For:     namesToChips(rec.For, photos),
		Against: namesToChips(rec.Against, photos),
		Absent:  namesToChips(rec.Absent, photos),
		Tally:   fmt.Sprintf("%d–%d", yea, nay),
	}

	switch result {
	case "CARRIED":
		v.HeadlineCls = "carried"
		v.Headline = "CARRIED"
	case "LOST":
		v.HeadlineCls = "lost"
		v.Headline = "LOST"
	case "TIE":
		v.HeadlineCls = "tie"
		v.Headline = "TIED"
	default:
		v.HeadlineCls = "carried"
		v.Headline = strings.ToUpper(result)
	}

	if yea > 0 && nay == 0 {
		v.Unanimous = true
		v.UnanimousFor = true
		v.Headline += " UNANIMOUSLY"
	} else if nay > 0 && yea == 0 {
		v.Unanimous = true
		v.UnanimousFor = false
		v.Headline += " UNANIMOUSLY"
	}

	return v
}

func namesToChips(names []string, photos map[string]string) []components.VoterChip {
	if len(names) == 0 {
		return nil
	}
	out := make([]components.VoterChip, len(names))
	for i, n := range names {
		out[i] = components.VoterChip{
			Name:     n,
			Initials: Initials(n),
			Photo:    photos[n], // empty when missing — template falls back to initials
		}
	}
	return out
}
