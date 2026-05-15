package council

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// LastScrapedAt returns the most recent scraped_at across all council meetings.
// Used by the about page to show when we last successfully pulled from eScribe.
// Returns zero time if no meetings have ever been scraped.
func (s *Store) LastScrapedAt(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := s.db.QueryRow(ctx, `SELECT MAX(scraped_at) FROM council_meetings`).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return t, err
}

// MeetingFilter specifies filter criteria for the meeting list.
type MeetingFilter struct {
	Term          string // "2018-2022", "2022-2026", or ""
	Query         string // full-text search across motions
	RecordedVotes bool   // only meetings with recorded votes
	Limit         int
	Offset        int
}

// MeetingSummary is a meeting row with aggregate motion/vote counts.

type MeetingSummary struct {
	ID            string
	Date          string
	Title         string
	Term          string
	MinutesURL    string
	HasVideo      bool
	Summary       string
	MotionCount   int
	RecordedVotes int
	CarriedCount  int
	LostCount     int
	MediaCount    int
}

// ListMeetingSummaries returns meetings with motion counts, filterable and paginated.
// When Query is set, only meetings containing matching motions are returned.

func (s *Store) ListMeetingSummaries(ctx context.Context, f MeetingFilter) ([]MeetingSummary, int, error) {
	if f.Limit <= 0 {
		f.Limit = 25
	}

	where := "WHERE 1=1"
	args := []any{}
	argN := 0

	nextArg := func(v any) string {
		argN++
		args = append(args, v)
		return fmt.Sprintf("$%d", argN)
	}

	if f.Term != "" {
		where += " AND m.term = " + nextArg(f.Term)
	}
	if f.Query != "" {
		where += " AND EXISTS (SELECT 1 FROM council_motions mo WHERE mo.meeting_id = m.id AND mo.search_text @@ plainto_tsquery('english', " + nextArg(f.Query) + "))"
	}
	if f.RecordedVotes {
		where += " AND EXISTS (SELECT 1 FROM council_motions mo WHERE mo.meeting_id = m.id AND mo.raw_text != '')"
	}

	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM council_meetings m `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limitArg := nextArg(f.Limit)
	offsetArg := nextArg(f.Offset)

	sql := fmt.Sprintf(`
		SELECT m.id, m.date::text, m.title, m.term, COALESCE(m.minutes_url, ''),
		       m.has_video,
		       COALESCE(m.llm_summary, ''),
		       (SELECT count(*) FROM council_motions mo WHERE mo.meeting_id = m.id),
		       (SELECT count(*) FROM council_motions mo WHERE mo.meeting_id = m.id AND mo.raw_text != ''),
		       (SELECT count(*) FROM council_motions mo WHERE mo.meeting_id = m.id AND mo.result = 'CARRIED'),
		       (SELECT count(*) FROM council_motions mo WHERE mo.meeting_id = m.id AND mo.result = 'LOST'),
		       (SELECT count(*) FROM council_motions mo WHERE mo.meeting_id = m.id AND mo.media_url IS NOT NULL)
		FROM council_meetings m
		%s
		ORDER BY m.date DESC
		LIMIT %s OFFSET %s`, where, limitArg, offsetArg)

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var meetings []MeetingSummary
	for rows.Next() {
		var ms MeetingSummary
		if err := rows.Scan(&ms.ID, &ms.Date, &ms.Title, &ms.Term, &ms.MinutesURL,
			&ms.HasVideo, &ms.Summary, &ms.MotionCount, &ms.RecordedVotes, &ms.CarriedCount, &ms.LostCount,
			&ms.MediaCount); err != nil {
			return nil, 0, err
		}
		meetings = append(meetings, ms)
	}
	return meetings, total, rows.Err()
}

// MeetingDetail is a single meeting with all its motions and vote records.

type MeetingDetail struct {
	ID         string
	Date       string
	Title      string
	Term       string
	MinutesURL string
	HasVideo   bool
	Summary    string // LLM meeting summary (same field used on the minutes index)
	Motions    []MotionRow
}

// MeetingSlug builds a URL-safe slug from a meeting title and date.
// e.g. "City Council" + "2026-02-17" → "city-council-2026-02-17"

func MeetingSlug(title, date string) string {
	s := strings.ToLower(title)
	s = strings.ReplaceAll(s, " ", "-")
	return s + "-" + date
}

// ParseMeetingSlug extracts title prefix and date from a slug.
// "city-council-2026-02-17" → ("city council", "2026-02-17")

func ParseMeetingSlug(slug string) (title, date string, ok bool) {
	// Date is always the last 10 chars: YYYY-MM-DD
	if len(slug) < 11 || slug[len(slug)-11] != '-' {
		return "", "", false
	}
	date = slug[len(slug)-10:]
	// Validate date format
	if len(date) != 10 || date[4] != '-' || date[7] != '-' {
		return "", "", false
	}
	prefix := slug[:len(slug)-11]
	title = strings.ReplaceAll(prefix, "-", " ")
	return title, date, true
}

// GetMeetingBySlug returns a meeting by its slug (e.g. "city-council-2026-02-17").

func (s *Store) GetMeetingBySlug(ctx context.Context, slug string) (*MeetingDetail, error) {
	title, date, ok := ParseMeetingSlug(slug)
	if !ok {
		return nil, pgx.ErrNoRows
	}
	md := &MeetingDetail{}
	err := s.db.QueryRow(ctx, `
		SELECT id, date::text, title, term, COALESCE(minutes_url, ''), has_video, COALESCE(llm_summary, '')
		FROM council_meetings WHERE date = $1::date AND LOWER(title) = $2 ORDER BY id LIMIT 1`, date, title,
	).Scan(&md.ID, &md.Date, &md.Title, &md.Term, &md.MinutesURL, &md.HasVideo, &md.Summary)
	if err != nil {
		return nil, err
	}
	return s.loadMeetingMotions(ctx, md)
}

// GetMeetingByID returns a single meeting with all its motions + vote summaries.

func (s *Store) GetMeetingByID(ctx context.Context, id string) (*MeetingDetail, error) {
	md := &MeetingDetail{}
	err := s.db.QueryRow(ctx, `
		SELECT id, date::text, title, term, COALESCE(minutes_url, ''), has_video, COALESCE(llm_summary, '')
		FROM council_meetings WHERE id = $1`, id,
	).Scan(&md.ID, &md.Date, &md.Title, &md.Term, &md.MinutesURL, &md.HasVideo, &md.Summary)
	if err != nil {
		return nil, err
	}
	return s.loadMeetingMotions(ctx, md)
}

func (s *Store) loadMeetingMotions(ctx context.Context, md *MeetingDetail) (*MeetingDetail, error) {
	rows, err := s.db.Query(ctx, `
		SELECT mo.id, m.date::text, m.term, m.id, COALESCE(m.minutes_url, ''),
		       mo.agenda_item, COALESCE(mo.llm_label, ''), COALESCE(mo.llm_summary, ''), mo.moved_by, mo.seconded_by, mo.motion_text, mo.result,
		       mo.media_url,
		       COALESCE((SELECT count(*) FROM council_vote_records r WHERE r.motion_id = mo.id AND r.position = 'for'), 0),
		       COALESCE((SELECT count(*) FROM council_vote_records r WHERE r.motion_id = mo.id AND r.position = 'against'), 0)
		FROM council_motions mo
		JOIN council_meetings m ON m.id = mo.meeting_id
		WHERE mo.meeting_id = $1
		ORDER BY mo.motion_index`, md.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var mr MotionRow
		if err := rows.Scan(&mr.ID, &mr.Date, &mr.Term, &mr.MeetingID, &mr.MinutesURL,
			&mr.AgendaItem, &mr.Label, &mr.Summary, &mr.MovedBy, &mr.SecondedBy, &mr.Text, &mr.Result,
			&mr.MediaURL, &mr.YeaCount, &mr.NayCount); err != nil {
			return nil, err
		}
		md.Motions = append(md.Motions, mr)
	}
	return md, rows.Err()
}

// MotionFilter specifies search and filter criteria for motions.

type MotionFilter struct {
	Term   string // "2018-2022", "2022-2026", or "" for all
	Result string // "CARRIED", "LOST", or ""
	Query  string // full-text search query
	Limit  int
	Offset int
}

// MotionRow is a motion joined with its meeting context and vote summary.

type MotionRow struct {
	ID         int64
	Date       string
	Term       string
	MeetingID  string
	MinutesURL string
	AgendaItem string
	Label      string // LLM-generated short title, fallback when AgendaItem empty
	Summary    string
	MovedBy    string
	SecondedBy string
	Text       string
	Result     string
	MediaURL   *string
	YeaCount   int
	NayCount   int
	Votes      *VoteRecord // populated on demand
}

// LoadVoteRecords loads vote records for a motion by ID (exported for handlers).

func (s *Store) LoadVoteRecords(ctx context.Context, motionID int64) (*VoteRecord, error) {
	return s.loadVoteRecords(ctx, motionID)
}

// MeetingIDsByDates returns a map from date string (YYYY-MM-DD) to meeting ID
// for all council_meetings whose date matches one of the given dates.

func (s *Store) MeetingIDsByDates(ctx context.Context, dates []string) (map[string]string, error) {
	if len(dates) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, date::text FROM council_meetings WHERE date::text = ANY($1)`, dates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string, len(dates))
	for rows.Next() {
		var id, d string
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		result[d] = id
	}
	return result, rows.Err()
}

// SearchMotions returns motions matching the filter, with total count for pagination.

func (s *Store) SearchMotions(ctx context.Context, f MotionFilter) ([]MotionRow, int, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}

	// Build WHERE clauses dynamically
	where := "WHERE 1=1"
	args := []any{}
	argN := 0

	nextArg := func(v any) string {
		argN++
		args = append(args, v)
		return fmt.Sprintf("$%d", argN)
	}

	if f.Term != "" {
		where += " AND m.term = " + nextArg(f.Term)
	}
	if f.Result != "" {
		where += " AND mo.result = " + nextArg(f.Result)
	}
	if f.Query != "" {
		where += " AND mo.search_text @@ plainto_tsquery('english', " + nextArg(f.Query) + ")"
	}

	// Count total
	var total int
	countSQL := `SELECT count(*) FROM council_motions mo JOIN council_meetings m ON m.id = mo.meeting_id ` + where
	if err := s.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting motions: %w", err)
	}

	// Fetch page with vote counts
	orderBy := "ORDER BY m.date DESC, mo.motion_index"
	if f.Query != "" {
		orderBy = fmt.Sprintf("ORDER BY ts_rank(mo.search_text, plainto_tsquery('english', $%d)) DESC, m.date DESC", argN)
	}

	limitArg := nextArg(f.Limit)
	offsetArg := nextArg(f.Offset)

	dataSQL := fmt.Sprintf(`
		SELECT mo.id, m.date::text, m.term, m.id, COALESCE(m.minutes_url, ''),
		       mo.agenda_item, COALESCE(mo.llm_label, ''), COALESCE(mo.llm_summary, ''), mo.moved_by, mo.seconded_by, mo.motion_text, mo.result,
		       mo.media_url,
		       COALESCE((SELECT count(*) FROM council_vote_records r WHERE r.motion_id = mo.id AND r.position = 'for'), 0),
		       COALESCE((SELECT count(*) FROM council_vote_records r WHERE r.motion_id = mo.id AND r.position = 'against'), 0)
		FROM council_motions mo
		JOIN council_meetings m ON m.id = mo.meeting_id
		%s %s LIMIT %s OFFSET %s`, where, orderBy, limitArg, offsetArg)

	rows, err := s.db.Query(ctx, dataSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying motions: %w", err)
	}
	defer rows.Close()

	var motions []MotionRow
	for rows.Next() {
		var mr MotionRow
		if err := rows.Scan(&mr.ID, &mr.Date, &mr.Term, &mr.MeetingID, &mr.MinutesURL,
			&mr.AgendaItem, &mr.Label, &mr.Summary, &mr.MovedBy, &mr.SecondedBy, &mr.Text, &mr.Result,
			&mr.MediaURL, &mr.YeaCount, &mr.NayCount); err != nil {
			return nil, 0, err
		}
		motions = append(motions, mr)
	}
	return motions, total, rows.Err()
}

// MotionDetail is a single motion with full vote records.

type MotionDetail struct {
	MotionRow
	RawText string
	Votes   *VoteRecord
}

// GetMotion returns a single motion by ID with vote records.

func (s *Store) GetMotion(ctx context.Context, id int64) (*MotionDetail, error) {
	md := &MotionDetail{}
	err := s.db.QueryRow(ctx, `
		SELECT mo.id, m.date::text, m.term, m.id, COALESCE(m.minutes_url, ''),
		       mo.agenda_item, COALESCE(mo.llm_summary, ''), mo.moved_by, mo.seconded_by, mo.motion_text, mo.result,
		       mo.media_url, mo.raw_text
		FROM council_motions mo
		JOIN council_meetings m ON m.id = mo.meeting_id
		WHERE mo.id = $1`, id,
	).Scan(&md.ID, &md.Date, &md.Term, &md.MeetingID, &md.MinutesURL,
		&md.AgendaItem, &md.Summary, &md.MovedBy, &md.SecondedBy, &md.Text, &md.Result,
		&md.MediaURL, &md.RawText)
	if err != nil {
		return nil, err
	}

	// Load vote counts
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(count(*) FILTER (WHERE position='for'), 0),
		       COALESCE(count(*) FILTER (WHERE position='against'), 0)
		FROM council_vote_records WHERE motion_id = $1`, id,
	).Scan(&md.YeaCount, &md.NayCount); err != nil {
		log.Warn("vote count query failed", "motion_id", id, "err", err)
	}

	// Load vote records
	md.Votes, _ = s.loadVoteRecords(ctx, id)

	return md, nil
}

// MotionStats returns aggregate counts for a term (or all if term is empty).

func (s *Store) MotionStats(ctx context.Context, term string) (meetings, totalMotions, recordedVotes int, err error) {
	if term == "" {
		err = s.db.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM council_meetings),
			       count(*),
			       count(*) FILTER (WHERE raw_text != '')
			FROM council_motions`).Scan(&meetings, &totalMotions, &recordedVotes)
	} else {
		err = s.db.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM council_meetings WHERE term = $1),
			       count(*),
			       count(*) FILTER (WHERE raw_text != '')
			FROM council_motions mo
			JOIN council_meetings m ON m.id = mo.meeting_id
			WHERE m.term = $1`, term).Scan(&meetings, &totalMotions, &recordedVotes)
	}
	return
}

// UnsummarizedMotion is a motion needing LLM summarization.

type UnsummarizedMotion struct {
	ID         int64
	AgendaItem string
	Text       string
	Result     string
	YeaCount   int
	NayCount   int
}

// MotionSummaryUpdate holds LLM-generated fields to write back.

func (s *Store) ListUnsummarized(ctx context.Context, term string, motionID int64, force bool) ([]UnsummarizedMotion, error) {
	where := "WHERE 1=1"
	args := []any{}
	argN := 0

	nextArg := func(v any) string {
		argN++
		args = append(args, v)
		return fmt.Sprintf("$%d", argN)
	}

	if motionID > 0 {
		where += " AND mo.id = " + nextArg(motionID)
	}
	if term != "" {
		where += " AND m.term = " + nextArg(term)
	}
	if !force {
		where += " AND mo.llm_summary = ''"
	}

	sql := fmt.Sprintf(`
		SELECT mo.id, COALESCE(mo.agenda_item, ''), mo.motion_text, mo.result,
		       COALESCE((SELECT count(*) FROM council_vote_records r WHERE r.motion_id = mo.id AND r.position = 'for'), 0),
		       COALESCE((SELECT count(*) FROM council_vote_records r WHERE r.motion_id = mo.id AND r.position = 'against'), 0)
		FROM council_motions mo
		JOIN council_meetings m ON m.id = mo.meeting_id
		%s
		ORDER BY m.date, mo.motion_index`, where)

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var motions []UnsummarizedMotion
	for rows.Next() {
		var m UnsummarizedMotion
		if err := rows.Scan(&m.ID, &m.AgendaItem, &m.Text, &m.Result, &m.YeaCount, &m.NayCount); err != nil {
			return nil, err
		}
		motions = append(motions, m)
	}
	return motions, rows.Err()
}

// UpdateMotionSummary writes LLM-generated summary fields for a motion.

type UnsummarizedMeetingMotion struct {
	Label   string
	Summary string
	Result  string
}

// UnsummarizedMeeting is a meeting needing an LLM summary.

type UnsummarizedMeeting struct {
	ID      string
	Date    string
	Title   string
	Motions []UnsummarizedMeetingMotion
}

// ListUnsummarizedMeetings returns meetings without LLM summaries, with their motion context.

func (s *Store) ListUnsummarizedMeetings(ctx context.Context, term string, force bool) ([]UnsummarizedMeeting, error) {
	where := "WHERE 1=1"
	args := []any{}
	argN := 0

	nextArg := func(v any) string {
		argN++
		args = append(args, v)
		return fmt.Sprintf("$%d", argN)
	}

	if term != "" {
		where += " AND m.term = " + nextArg(term)
	}
	if !force {
		where += " AND m.llm_summary = ''"
	}

	sql := fmt.Sprintf(`
		SELECT m.id, m.date::text, m.title
		FROM council_meetings m
		%s
		ORDER BY m.date`, where)

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var meetings []UnsummarizedMeeting
	for rows.Next() {
		var m UnsummarizedMeeting
		if err := rows.Scan(&m.ID, &m.Date, &m.Title); err != nil {
			return nil, err
		}
		meetings = append(meetings, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load motion context for each meeting
	for i := range meetings {
		motRows, err := s.db.Query(ctx, `
			SELECT COALESCE(NULLIF(llm_label, ''), COALESCE(NULLIF(agenda_item, ''), LEFT(motion_text, 60))),
			       COALESCE(llm_summary, ''), result
			FROM council_motions
			WHERE meeting_id = $1
			ORDER BY motion_index`, meetings[i].ID)
		if err != nil {
			return nil, fmt.Errorf("loading motions for %s: %w", meetings[i].ID, err)
		}

		for motRows.Next() {
			var mm UnsummarizedMeetingMotion
			if err := motRows.Scan(&mm.Label, &mm.Summary, &mm.Result); err != nil {
				motRows.Close()
				return nil, err
			}
			meetings[i].Motions = append(meetings[i].Motions, mm)
		}
		motRows.Close()
		if err := motRows.Err(); err != nil {
			return nil, err
		}
	}

	return meetings, nil
}

// UpdateMeetingSummary writes an LLM-generated summary for a meeting.

func (s *Store) LoadMeetings(ctx context.Context, term string) ([]Meeting, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, date::text, title, COALESCE(minutes_url, ''), COALESCE(pdf_file, ''),
		       COALESCE(llm_summary, '')
		FROM council_meetings
		WHERE term = $1
		ORDER BY date`, term)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var meetings []Meeting
	for rows.Next() {
		var m Meeting
		if err := rows.Scan(&m.ID, &m.Date, &m.Title, &m.MinutesURL, &m.PDFFile, &m.Summary); err != nil {
			return nil, err
		}
		meetings = append(meetings, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range meetings {
		meetings[i].Motions, err = s.loadMotions(ctx, meetings[i].ID)
		if err != nil {
			return nil, fmt.Errorf("loading motions for %s: %w", meetings[i].ID, err)
		}
	}

	return meetings, nil
}

func (s *Store) loadMotions(ctx context.Context, meetingID string) ([]Motion, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, motion_text, moved_by, seconded_by, result, raw_text,
		       COALESCE(agenda_item, ''), COALESCE(llm_summary, ''), COALESCE(llm_label, ''),
		       COALESCE(media_url, '')
		FROM council_motions
		WHERE meeting_id = $1
		ORDER BY motion_index`, meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type motionWithID struct {
		id int64
		Motion
	}
	var mots []motionWithID
	for rows.Next() {
		var m motionWithID
		if err := rows.Scan(&m.id, &m.Text, &m.MovedBy, &m.SecondedBy, &m.Result, &m.RawText,
			&m.AgendaItem, &m.Summary, &m.Label, &m.MediaURL); err != nil {
			return nil, err
		}
		mots = append(mots, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	motions := make([]Motion, len(mots))
	for i, m := range mots {
		motions[i] = m.Motion
		records, err := s.loadVoteRecords(ctx, m.id)
		if err != nil {
			return nil, err
		}
		if records != nil {
			motions[i].Votes = records
		}
	}

	return motions, nil
}

func (s *Store) loadVoteRecords(ctx context.Context, motionID int64) (*VoteRecord, error) {
	rows, err := s.db.Query(ctx, `
		SELECT councillor, position
		FROM council_vote_records
		WHERE motion_id = $1
		ORDER BY councillor`, motionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vr := &VoteRecord{}
	any := false
	for rows.Next() {
		var name, pos string
		if err := rows.Scan(&name, &pos); err != nil {
			return nil, err
		}
		any = true
		switch pos {
		case "for":
			vr.For = append(vr.For, name)
		case "against":
			vr.Against = append(vr.Against, name)
		case "absent":
			vr.Absent = append(vr.Absent, name)
		}
	}
	if !any {
		return nil, rows.Err()
	}
	return vr, rows.Err()
}

// CouncillorVoteStats holds aggregate voting counts for a single councillor.

type CouncillorVoteStats struct {
	ForCount     int
	AgainstCount int
	AbsentCount  int
}

// TotalRecorded returns the total number of recorded votes this councillor appeared in.

func (s CouncillorVoteStats) TotalRecorded() int {
	return s.ForCount + s.AgainstCount + s.AbsentCount
}

// VotesCast returns votes where the councillor was present (for + against).

func (s CouncillorVoteStats) VotesCast() int {
	return s.ForCount + s.AgainstCount
}

// VoteMatrixMotion is a column header in the vote matrix.

type VoteMatrixMotion struct {
	ID         int64
	MeetingID  string
	Date       string
	AgendaItem string // truncated for grid display
	Summary    string // LLM summary for modal
	FullTitle  string // full agenda item for modal
	Result     string
	MediaURL   string
}

// VoteMatrixRecord is a single cell: how one councillor voted on one motion.

type VoteMatrixRecord struct {
	MotionID   int64
	Councillor string
	Position   string // for, against, absent
}

// VoteMatrix returns the data needed to build a councillor × motion grid.

func (s *Store) VoteMatrix(ctx context.Context, term string) ([]VoteMatrixMotion, []VoteMatrixRecord, error) {
	// Get all motions with recorded votes
	mRows, err := s.db.Query(ctx, `
		SELECT id, meeting_id, date, label, summary, full_title, result, media_url
		FROM (
			SELECT DISTINCT ON (mo.meeting_id, COALESCE(NULLIF(mo.llm_label, ''), LEFT(COALESCE(NULLIF(mo.agenda_item, ''), mo.motion_text), 60)))
			       mo.id, m.id AS meeting_id, m.date::text AS date,
			       COALESCE(NULLIF(mo.llm_label, ''), LEFT(COALESCE(NULLIF(mo.agenda_item, ''), mo.motion_text), 60)) AS label,
			       COALESCE(mo.llm_summary, '') AS summary,
			       COALESCE(NULLIF(mo.agenda_item, ''), LEFT(mo.motion_text, 200)) AS full_title,
			       mo.result,
			       COALESCE(mo.media_url, '') AS media_url,
			       mo.motion_index
			FROM council_motions mo
			JOIN council_meetings m ON m.id = mo.meeting_id
			WHERE m.term = $1 AND mo.raw_text != ''
			ORDER BY mo.meeting_id, COALESCE(NULLIF(mo.llm_label, ''), LEFT(COALESCE(NULLIF(mo.agenda_item, ''), mo.motion_text), 60)), mo.motion_index
		) deduped
		ORDER BY date DESC, motion_index`, term)
	if err != nil {
		return nil, nil, err
	}
	defer mRows.Close()

	var motions []VoteMatrixMotion
	for mRows.Next() {
		var m VoteMatrixMotion
		if err := mRows.Scan(&m.ID, &m.MeetingID, &m.Date, &m.AgendaItem, &m.Summary, &m.FullTitle, &m.Result, &m.MediaURL); err != nil {
			return nil, nil, err
		}
		motions = append(motions, m)
	}
	if err := mRows.Err(); err != nil {
		return nil, nil, err
	}

	// Get all vote records for those motions
	vRows, err := s.db.Query(ctx, `
		SELECT vr.motion_id, vr.councillor, vr.position
		FROM council_vote_records vr
		JOIN council_motions mo ON mo.id = vr.motion_id
		JOIN council_meetings m ON m.id = mo.meeting_id
		WHERE m.term = $1 AND mo.raw_text != ''
		ORDER BY vr.councillor`, term)
	if err != nil {
		return nil, nil, err
	}
	defer vRows.Close()

	var records []VoteMatrixRecord
	for vRows.Next() {
		var r VoteMatrixRecord
		if err := vRows.Scan(&r.MotionID, &r.Councillor, &r.Position); err != nil {
			return nil, nil, err
		}
		records = append(records, r)
	}
	return motions, records, vRows.Err()
}

// CouncillorVoteStatsAll returns per-councillor vote aggregates for a term.

func (s *Store) CouncillorVoteStatsAll(ctx context.Context, term string) (map[string]CouncillorVoteStats, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			vr.councillor,
			COUNT(*) FILTER (WHERE vr.position = 'for') AS vote_for,
			COUNT(*) FILTER (WHERE vr.position = 'against') AS vote_against,
			COUNT(*) FILTER (WHERE vr.position = 'absent') AS vote_absent
		FROM council_vote_records vr
		JOIN council_motions mo ON mo.id = vr.motion_id
		JOIN council_meetings m ON m.id = mo.meeting_id
		WHERE m.term = $1
		GROUP BY vr.councillor`, term)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]CouncillorVoteStats)
	for rows.Next() {
		var name string
		var cs CouncillorVoteStats
		if err := rows.Scan(&name, &cs.ForCount, &cs.AgainstCount, &cs.AbsentCount); err != nil {
			return nil, err
		}
		stats[name] = cs
	}
	return stats, rows.Err()
}
