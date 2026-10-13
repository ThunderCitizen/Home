package transit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/transit/chunk"
)

// RouteDaySummary is one persisted route/service-day tuple. Additive inputs
// preserve the metric definitions; displayed percentages are never averaged.
type RouteDaySummary struct {
	chunk.Quality
	RouteID               string
	Date                  time.Time
	Trips                 int
	Scheduled             int
	Cancelled             int
	NoNotice              int
	CancellationScheduled int
	ScheduleID            *string
	BuiltAt               time.Time
	IsFinal               bool
}

// CardStats renders the daily tuple with the same reducer as Metrics. Bands
// lacking telemetry must not inflate the cancellation denominator on collapse.
func (d RouteDaySummary) CardStats() RouteRowKPI {
	if d.Version != chunk.Version {
		return RouteRowKPI{}
	}
	view := chunk.ChunkView{Quality: d.Quality, RouteID: d.RouteID, Trips: d.Trips,
		Scheduled: d.CancellationScheduled, Cancelled: d.Cancelled, NoNotice: d.NoNotice}
	return RouteRowKPIFromChunks([]chunk.ChunkView{view}, d.RouteID)
}

// RouteDaySummaries reads the small retained table, never stop/event detail.
func (s *Service) RouteDaySummaries(ctx context.Context, date time.Time) (map[string]RouteDaySummary, error) {
	return s.reporter.repo.RouteDaySummaries(ctx, date)
}
func (r *Repo) RouteDaySummaries(ctx context.Context, date time.Time) (map[string]RouteDaySummary, error) {
	rows, err := r.db.Query(ctx, `SELECT route_id,date,metric_version,
  trip_count,scheduled_count,cancelled_count,no_notice_count,cancellation_scheduled_count,
  otp_count,otp_on_time,early_count,late_count,expected_timepoints,observed_timepoints,
  eligible_windows,total_windows,wait_observed_area,wait_scheduled_area,window_seconds,cv_weighted_sum,cv_weight,
  schedule_id,built_at,is_final FROM transit.route_day_summary WHERE date=$1::date ORDER BY route_id`, DateOnly(date))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]RouteDaySummary{}
	for rows.Next() {
		var d RouteDaySummary
		if err := rows.Scan(&d.RouteID, &d.Date, &d.Version, &d.Trips, &d.Scheduled, &d.Cancelled, &d.NoNotice, &d.CancellationScheduled,
			&d.OTPCount, &d.OTPOnTime, &d.Early, &d.Late, &d.ExpectedTimepoints, &d.ObservedTimepoints, &d.EligibleWindows, &d.TotalWindows,
			&d.WaitObservedArea, &d.WaitScheduledArea, &d.WindowSeconds, &d.CVWeightedSum, &d.CVWeight, &d.ScheduleID, &d.BuiltAt, &d.IsFinal); err != nil {
			return nil, err
		}
		out[d.RouteID] = d
	}
	return out, rows.Err()
}

// RefreshRouteDaySummary repairs only the daily projection from retained bands.
// It is safe to use without raw observations; it never recalculates recipes.
func RefreshRouteDaySummary(ctx context.Context, db *pgxpool.Pool, date time.Time) error {
	if _, err := db.Exec(ctx, `SELECT transit.refresh_route_day_summary($1::date)`, DateOnly(date)); err != nil {
		return fmt.Errorf("refresh route/day summaries: %w", err)
	}
	return nil
}

// RouteSummarySinceDate keeps daily navigation independent of band/event reads.
func (s *Service) RouteSummarySinceDate(ctx context.Context) (string, error) {
	var first *time.Time
	if err := s.db.QueryRow(ctx, `SELECT min(date) FROM transit.route_day_summary`).Scan(&first); err != nil {
		return "", err
	}
	if first == nil {
		return "", nil
	}
	return first.Format("2006-01-02"), nil
}

// RouteDayTotals pools available route counts before calculating the rate.
// Missing/legacy routes never contribute invented zero observations.
type RouteDayTotals struct {
	Trips     string
	Cancelled string
	CancelPct string
}

func TotalRouteDayStats(summaries map[string]RouteDaySummary) RouteDayTotals {
	totals := RouteDayTotals{Trips: "—", Cancelled: "—", CancelPct: "—"}
	trips, cancelled, scheduled := 0, 0, 0
	observed := false
	for _, d := range summaries {
		if d.Version != chunk.Version || (d.Trips == 0 && d.Cancelled == 0) {
			continue
		}
		observed = true
		trips += d.Trips
		cancelled += d.Cancelled
		scheduled += d.CancellationScheduled
	}
	if !observed {
		return totals
	}
	totals.Trips = fmt.Sprint(trips)
	totals.Cancelled = fmt.Sprint(cancelled)
	if scheduled > 0 {
		totals.CancelPct = fmt.Sprintf("%.1f%%", 100*float64(cancelled)/float64(scheduled))
	}
	return totals
}
