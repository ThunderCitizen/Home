package recipes

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thundercitizen/internal/transit/chunk"
)

// otpQuery counts trips and on-time trips for one (route, date, band).
//
// Methodology:
//
//   - A "trip" is one row in transit.stop_delay grouped by trip_id.
//     Only timepoint stops count — timepoints are the schedule-adherence
//     checkpoints; non-timepoint stops aren't held to a published time.
//     Timepoint membership is resolved via EXISTS against
//     transit.route_pattern_stop (keyed by pattern_id + stop_id); the
//     recorder leaves stop_delay.is_timepoint false because its trip
//     cache isn't keyed by stop, so every timepoint-filtering query
//     joins back to the pattern table. Mirrors the same pattern used
//     by headway.go.
//   - The trip's delay is the average of arrival_delay across its
//     timepoint stops, falling back to departure_delay when arrival is
//     null.
//   - A trip is "on time" when its average delay is in
//     [chunk.OTPEarlyLimit, chunk.OTPLateLimit] — the industry-standard
//     -1 minute / +5 minutes window. The constants live in
//     internal/transit/chunk/chunk.go so the math package and the SQL
//     parameter binding share one source of truth.
//
// The result feeds chunk.ChunkView.OTPPct via on_time / trips * 100.
const otpQuery = `
SELECT
    COUNT(*)::int AS trip_count,
    COUNT(*) FILTER (WHERE avg_delay >= $4 AND avg_delay <= $5)::int AS on_time_count
FROM (
    SELECT d.trip_id, AVG(COALESCE(d.arrival_delay, d.departure_delay)) AS avg_delay
    FROM transit.stop_delay d
    WHERE d.date = $1::date AND d.route_id = $2 AND d.band = $3
      AND EXISTS (
        SELECT 1 FROM transit.route_pattern_stop rps
        WHERE rps.pattern_id = d.pattern_id
          AND rps.stop_id = d.stop_id
          AND rps.is_timepoint = true
      )
    GROUP BY d.trip_id
) t
`

// OTPResult is what one OTP recipe call returns.
type OTPResult struct {
	Trips  int // distinct trips with at least one timepoint observation
	OnTime int // trips whose average timepoint delay is within the OTP window
}

// OTP returns the trip count and on-time count for one chunk.
//
// To compute the OTP percentage from a single chunk: OnTime * 100 / Trips.
// To compute system OTP across many chunks: SUM(on_time) / SUM(trips) * 100.
// Aggregating the percentage instead of the counts is wrong (mean of
// percentages != trip-weighted percentage); always sum the raw counts.
func OTP(ctx context.Context, db *pgxpool.Pool, routeID string, date time.Time, band string) (OTPResult, error) {
	var r OTPResult
	err := db.QueryRow(ctx, otpQuery,
		date,                // $1
		routeID,             // $2
		band,                // $3
		chunk.OTPEarlyLimit, // $4 — -60 sec (1 min early)
		chunk.OTPLateLimit,  // $5 — +300 sec (5 min late)
	).Scan(&r.Trips, &r.OnTime)
	return r, err
}
