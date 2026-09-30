package transit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thundercitizen/internal/transit/chunk"
)

// BuildChunk uses the same daily recipes as the persisted rollup.
func BuildChunk(ctx context.Context, db *pgxpool.Pool, routeID string, date time.Time, b Band) (*chunk.Chunk, error) {
	_, chunks, err := buildMetricDay(ctx, db, date)
	if err != nil {
		return nil, err
	}
	for _, c := range chunks {
		if c.RouteID == routeID && c.Band == b.Name {
			return c, nil
		}
	}
	return nil, nil
}

// BuildChunksForDate atomically replaces a complete day and its recipe marker.
// A crash cannot leave a partially rebuilt day that looks complete at next boot.
func BuildChunksForDate(ctx context.Context, db *pgxpool.Pool, date time.Time) (int, error) {
	scheduleID, chunks, err := buildMetricDay(ctx, db, date)
	if err != nil {
		return 0, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(23002,($1::date-date '2000-01-01'))`, date); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM transit.route_band_chunk WHERE date=$1::date`, date); err != nil {
		return 0, err
	}
	for _, c := range chunks {
		_, err = tx.Exec(ctx, `INSERT INTO transit.route_band_chunk (
            route_id,date,band,service_kind,trip_count,on_time_count,scheduled_count,cancelled_count,no_notice_count,
            headway_count,headway_sum_sec,headway_sum_sec_sq,sched_headway_sec,
            metric_version,otp_count,otp_on_time,early_count,late_count,expected_timepoints,observed_timepoints,
            eligible_windows,total_windows,wait_observed_area,wait_scheduled_area,window_seconds,cv_weighted_sum,cv_weight,built_at
        ) VALUES ($1,$2,$3,$4,$5,0,$6,$7,$8,0,0,0,0,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,now())`,
			c.RouteID, c.Date, c.Band, c.ServiceKind, c.TripCount, c.ScheduledCount, c.CancelledCount, c.NoNoticeCount,
			c.Version, c.OTPCount, c.OTPOnTime, c.Early, c.Late, c.ExpectedTimepoints, c.ObservedTimepoints,
			c.EligibleWindows, c.TotalWindows, c.WaitObservedArea, c.WaitScheduledArea, c.WindowSeconds, c.CVWeightedSum, c.CVWeight)
		if err != nil {
			return 0, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO transit.metric_rebuild(date,version,schedule_id) VALUES($1,$2,$3)
        ON CONFLICT(date) DO UPDATE SET version=excluded.version,schedule_id=excluded.schedule_id,built_at=now()`, date, chunk.Version, scheduleID)
	if err != nil {
		return 0, err
	}
	return len(chunks), tx.Commit(ctx)
}
