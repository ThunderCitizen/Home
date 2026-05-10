package council

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SaveMeetings upserts meetings and their motions into the database.
// Preserves manually set significance values on re-scrape.
func (s *Store) SaveMeetings(ctx context.Context, meetings []Meeting) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, m := range meetings {
		if err := s.saveMeeting(ctx, tx, m); err != nil {
			return fmt.Errorf("meeting %s (%s): %w", m.Date, m.ID, err)
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) saveMeeting(ctx context.Context, tx pgx.Tx, m Meeting) error {
	term := TermForDate(m.Date)

	_, err := tx.Exec(ctx, `
		INSERT INTO council_meetings (id, date, title, term, minutes_url, has_video, pdf_file, scraped_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (id) DO UPDATE SET
			minutes_url = EXCLUDED.minutes_url,
			has_video   = EXCLUDED.has_video,
			pdf_file    = EXCLUDED.pdf_file,
			scraped_at  = NOW()`,
		m.ID, m.Date, m.Title, term, m.MinutesURL, m.HasVideo, m.PDFFile)
	if err != nil {
		return fmt.Errorf("upsert meeting: %w", err)
	}

	for i, mot := range m.Motions {
		if err := s.saveMotion(ctx, tx, m.ID, i, mot); err != nil {
			return fmt.Errorf("motion %d: %w", i, err)
		}
	}

	// Remove motions beyond what we parsed (re-parse may yield fewer).
	_, err = tx.Exec(ctx,
		`DELETE FROM council_motions WHERE meeting_id = $1 AND motion_index >= $2`,
		m.ID, len(m.Motions))
	if err != nil {
		return fmt.Errorf("trimming stale motions: %w", err)
	}

	return nil
}

func (s *Store) saveMotion(ctx context.Context, tx pgx.Tx, meetingID string, idx int, mot Motion) error {
	var motionID int64
	err := tx.QueryRow(ctx, `
		INSERT INTO council_motions (meeting_id, motion_index, motion_text, moved_by, seconded_by, result, raw_text, agenda_item)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (meeting_id, motion_index) DO UPDATE SET
			motion_text = EXCLUDED.motion_text,
			moved_by    = EXCLUDED.moved_by,
			seconded_by = EXCLUDED.seconded_by,
			result      = EXCLUDED.result,
			raw_text    = EXCLUDED.raw_text,
			agenda_item = EXCLUDED.agenda_item
		RETURNING id`,
		meetingID, idx, mot.Text, mot.MovedBy, mot.SecondedBy, mot.Result, mot.RawText, mot.AgendaItem,
	).Scan(&motionID)
	if err != nil {
		return fmt.Errorf("upsert motion: %w", err)
	}

	// Re-sync vote records for this motion
	if _, err := tx.Exec(ctx, `DELETE FROM council_vote_records WHERE motion_id = $1`, motionID); err != nil {
		return fmt.Errorf("delete vote records: %w", err)
	}

	if mot.Votes == nil {
		return nil
	}

	batch := &pgx.Batch{}
	for _, name := range mot.Votes.For {
		batch.Queue(`INSERT INTO council_vote_records (motion_id, councillor, position) VALUES ($1, $2, 'for') ON CONFLICT DO NOTHING`,
			motionID, name)
	}
	for _, name := range mot.Votes.Against {
		batch.Queue(`INSERT INTO council_vote_records (motion_id, councillor, position) VALUES ($1, $2, 'against') ON CONFLICT DO NOTHING`,
			motionID, name)
	}
	for _, name := range mot.Votes.Absent {
		batch.Queue(`INSERT INTO council_vote_records (motion_id, councillor, position) VALUES ($1, $2, 'absent') ON CONFLICT DO NOTHING`,
			motionID, name)
	}

	if batch.Len() == 0 {
		return nil
	}

	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for range batch.Len() {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("insert vote record: %w", err)
		}
	}

	return nil
}

type MotionSummaryUpdate struct {
	ID           int64
	Summary      string
	Label        string
	Significance string
	Model        string
}

// ListUnsummarized returns motions without LLM summaries, optionally filtered by term or ID.

func (s *Store) UpdateMotionSummary(ctx context.Context, u MotionSummaryUpdate) error {
	_, err := s.db.Exec(ctx, `
		UPDATE council_motions
		SET llm_summary = $2, llm_label = $3, llm_significance = $4, llm_model = $5,
		    significance = CASE
		        WHEN significance IN ('', 'routine') THEN $4
		        ELSE significance
		    END
		WHERE id = $1`,
		u.ID, u.Summary, u.Label, u.Significance, u.Model)
	return err
}

// UnsummarizedMeetingMotion is a motion's summary data used to build a meeting-level prompt.

func (s *Store) UpdateMeetingSummary(ctx context.Context, meetingID, summary, model string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE council_meetings SET llm_summary = $2, llm_model = $3 WHERE id = $1`,
		meetingID, summary, model)
	return err
}

// LoadMeetings reads all meetings with their motions for a given term.
