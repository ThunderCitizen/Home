package transit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thundercitizen/internal/logger"
)

// ChunkRollup keeps transit.route_band_chunk populated without operator
// intervention. It pairs a one-shot startup backfill (build any missing
// days in the recent past) with a periodic rebuild of today's chunks so
// live metrics stay fresh. Every upsert is idempotent; running the
// fetcher's chunks subcommand or the seeder alongside this loop is safe.
//
// Conceptually: treat route_band_chunk like a materialised view over the
// event tables, rebuilt in ~6h blocks as each band closes. We don't
// literally use PG MVs because the source tables are append-only and the
// recipe queries are too chatty for REFRESH MATERIALIZED VIEW semantics;
// a few per-date upsert passes are cheap and easier to audit.
type ChunkRollup struct {
	db *pgxpool.Pool

	// backfillDays bounds how far back the startup scan reaches. Older
	// history is left untouched — run `./bin/fetcher chunks` to fill
	// deeper windows by hand.
	backfillDays int

	// interval is how often to rebuild today's chunks.
	interval time.Duration
}

var rollupLog = logger.New("chunk_rollup")

// NewChunkRollup builds a rollup with production defaults: a 60-day
// startup backfill window and a 10-minute refresh cadence.
func NewChunkRollup(db *pgxpool.Pool) *ChunkRollup {
	return &ChunkRollup{
		db:           db,
		backfillDays: 60,
		interval:     10 * time.Minute,
	}
}

// Start launches the rollup goroutine. Returns immediately; the goroutine
// exits when ctx is cancelled. Intentionally does NOT block boot — the
// backfill and first rebuild run asynchronously so slow queries can't
// delay the HTTP listener coming up.
func (r *ChunkRollup) Start(ctx context.Context) {
	go r.run(ctx)
}

func (r *ChunkRollup) run(ctx context.Context) {
	// Startup backfill: any recent date with events but no chunk row is
	// filled in once, in order (oldest first) so metric queries see
	// contiguous history as it becomes available.
	if err := r.backfill(ctx); err != nil {
		rollupLog.Error("startup backfill failed", "err", err)
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// First tick runs immediately so today's row is present right after
	// backfill finishes.
	r.rebuildToday(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.rebuildToday(ctx)
		}
	}
}

// backfill builds any date in [today-backfillDays, yesterday] that has
// events but no chunks. Today is intentionally skipped — rebuildToday
// owns that slot.
func (r *ChunkRollup) backfill(ctx context.Context) error {
	today := ServiceDate()
	from := today.AddDate(0, 0, -r.backfillDays)
	yesterday := today.AddDate(0, 0, -1)

	missing, err := r.findMissingDates(ctx, from, yesterday)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		rollupLog.Info("startup backfill: no missing days", "window_days", r.backfillDays)
		return nil
	}
	rollupLog.Info("startup backfill: building chunks", "days", len(missing), "from", missing[0].Format("2006-01-02"), "to", missing[len(missing)-1].Format("2006-01-02"))

	for _, d := range missing {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dayCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		n, err := BuildChunksForDate(dayCtx, r.db, d)
		cancel()
		if err != nil {
			rollupLog.Error("backfill build failed", "date", d.Format("2006-01-02"), "err", err)
			continue
		}
		rollupLog.Info("backfill built", "date", d.Format("2006-01-02"), "chunks", n)
	}
	return nil
}

// findMissingDates returns every date in [from, to] that appears in the
// event tables but not in route_band_chunk. Uses transit.stop_delay as
// the presence oracle — it's the densest of the event tables and covers
// any day with observed service.
func (r *ChunkRollup) findMissingDates(ctx context.Context, from, to time.Time) ([]time.Time, error) {
	const q = `
		WITH event_days AS (
			SELECT DISTINCT date
			FROM transit.stop_delay
			WHERE date BETWEEN $1 AND $2
		),
		chunk_days AS (
			SELECT DISTINCT date
			FROM transit.route_band_chunk
			WHERE date BETWEEN $1 AND $2
		)
		SELECT date FROM event_days
		WHERE date NOT IN (SELECT date FROM chunk_days)
		ORDER BY date
	`
	rows, err := r.db.Query(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// rebuildToday upserts every chunk for the current service date. Runs on
// the rollup's ticker; idempotent so it's fine if today's row is already
// populated (the upsert just overwrites with the same values).
func (r *ChunkRollup) rebuildToday(ctx context.Context) {
	today := ServiceDate()
	dayCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	n, err := BuildChunksForDate(dayCtx, r.db, today)
	if err != nil {
		rollupLog.Error("rebuild today failed", "date", today.Format("2006-01-02"), "err", err)
		return
	}
	rollupLog.Debug("rebuild today", "date", today.Format("2006-01-02"), "chunks", n)
}
