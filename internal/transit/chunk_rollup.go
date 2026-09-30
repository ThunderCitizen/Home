package transit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/logger"
	"thundercitizen/internal/transit/chunk"
)

var rollupLog = logger.New("chunk_rollup")

type ChunkRollup struct {
	db           *pgxpool.Pool
	backfillDays int
	interval     time.Duration
}

func NewChunkRollup(db *pgxpool.Pool) *ChunkRollup {
	return &ChunkRollup{db: db, backfillDays: 60, interval: 10 * time.Minute}
}
func (r *ChunkRollup) Start(ctx context.Context) { go r.run(ctx) }
func (r *ChunkRollup) run(ctx context.Context) {
	// On an existing installation the GTFS refresher may decide nothing has
	// changed; still archive the installed bundle after the history migration.
	if _, err := ImportMetricSchedule(ctx, r.db, gtfsBaseDir, "installed GTFS", time.Now()); err != nil {
		rollupLog.Error("archive timetable", "err", err)
	}
	r.rebuildToday(ctx)
	if err := r.backfill(ctx); err != nil {
		rollupLog.Error("backfill", "err", err)
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.backfill(ctx); err != nil {
				rollupLog.Error("backfill", "err", err)
			}
			r.rebuildToday(ctx)
		}
	}
}
func (r *ChunkRollup) backfill(ctx context.Context) error {
	today := ServiceDate()
	dates, err := r.findMissingDates(ctx, today.AddDate(0, 0, -r.backfillDays), today.AddDate(0, 0, -1))
	if err != nil {
		return err
	}
	for _, date := range dates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.rebuild(ctx, date)
	}
	return nil
}
func (r *ChunkRollup) findMissingDates(ctx context.Context, from, to time.Time) ([]time.Time, error) {
	rows, err := r.db.Query(ctx, `SELECT DISTINCT s.date FROM transit.metric_service s
        LEFT JOIN transit.metric_rebuild b ON b.date=s.date
        WHERE s.date <= $2::date
        AND (s.date >= $1::date OR EXISTS(
            SELECT 1 FROM transit.route_band_chunk c WHERE c.date=s.date AND c.metric_version<>$3
        ))
        AND (b.date IS NULL OR b.version<>$3 OR b.built_at < (s.date + interval '1 day 4 hours') AT TIME ZONE 'America/Thunder_Bay')
        AND (EXISTS(SELECT 1 FROM transit.stop_delay d WHERE d.date=s.date)
             OR EXISTS(SELECT 1 FROM transit.cancellation c WHERE c.start_date=to_char(s.date,'YYYYMMDD')))
        ORDER BY s.date`, from, to, chunk.Version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dates []time.Time
	for rows.Next() {
		var date time.Time
		if err := rows.Scan(&date); err != nil {
			return nil, err
		}
		dates = append(dates, time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, TZ))
	}
	return dates, rows.Err()
}
func (r *ChunkRollup) rebuildToday(ctx context.Context) { r.rebuild(ctx, ServiceDate()) }
func (r *ChunkRollup) rebuild(ctx context.Context, date time.Time) {
	dayCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := RebuildMetricPassages(dayCtx, r.db, date); err != nil {
		rollupLog.Error("rebuild passages", "date", date.Format("2006-01-02"), "err", err)
		return
	}
	if _, err := BuildChunksForDate(dayCtx, r.db, date); err != nil {
		rollupLog.Error("rebuild chunks", "date", date.Format("2006-01-02"), "err", err)
	}
}
