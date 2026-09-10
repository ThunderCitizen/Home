package transit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thundercitizen/internal/transit/chunk"
)

// Service loads transit data for each request and owns the live feed clients.
type Service struct {
	db       *pgxpool.Pool
	reporter *Reporter
	stream   *VehicleStream
	recorder *Recorder

	// Testability hooks — override in tests to avoid hitting the DB.
	getRoute             func(ctx context.Context, routeID string) (*RouteInfo, error)
	routeTimepointLoader func(ctx context.Context, routeID string, date time.Time) ([]TimepointSchedule, error)
}

// liveData bundles all data for the transit live page.
type liveData struct {
	dashboard *DashboardReport
	incidents []CancelIncident
	noService []string
}

// NewService creates a transit service with its dependencies. Pass the
// recorder so stop predictions can serve from its in-memory trip feed
// instead of re-fetching from the upstream API.
func NewService(db *pgxpool.Pool, recorder *Recorder) *Service {
	client := NewClient()
	reporter := NewReporter(db, client)
	return &Service{
		db:       db,
		reporter: reporter,
		stream:   NewVehicleStream(client, db, 6*time.Second),
		recorder: recorder,
	}
}

// RouteMeta reads metadata for routes with service in the past week.
func (s *Service) RouteMeta(ctx context.Context) []RouteMetaAPI {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	to := ServiceDate()
	v, err := s.reporter.repo.AllRouteMeta(ctx, to.AddDate(0, 0, -6), to)
	if err != nil {
		return nil
	}
	return v
}

// SinceDate returns the earliest date the database has chunks for,
// formatted as YYYY-MM-DD, or "" if the table is empty. Used by the
// date selector to disable the prev arrow at the data boundary.
func (s *Service) SinceDate(ctx context.Context) string {
	d, err := s.reporter.repo.EarliestChunkDate(ctx)
	if err != nil || d.IsZero() {
		return ""
	}
	return d.Format("2006-01-02")
}

// Chunks reads the existing rollup rows in [from, to] inclusive.
func (s *Service) Chunks(ctx context.Context, from, to time.Time) ([]chunk.ChunkView, error) {
	return s.reporter.repo.Chunks(ctx, from, to)
}

// Stats returns a stats report by variant ("day", "week", "percentiles").
func (s *Service) Stats(ctx context.Context, variant string) *StatsReport {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var report *StatsReport
	var err error
	switch variant {
	case "day":
		report, err = s.reporter.DayStats(ctx)
	case "percentiles":
		report, err = s.reporter.Percentiles(ctx)
	case "week":
		report, err = s.reporter.WeekStats(ctx)
	default:
		return nil
	}
	if err != nil || ctx.Err() != nil {
		return nil
	}
	return report
}

// LiveBusCount returns the route-assigned vehicle count from the latest
// stream frame, for server-rendering the "Buses" tab badge.
func (s *Service) LiveBusCount() int {
	return s.stream.LiveBusCount()
}

// CancelDetails reads the per-trip cancel log for [from, to] directly.
func (s *Service) CancelDetails(ctx context.Context, from, to time.Time) ([]CancelDetail, error) {
	return LoadCancelDetails(ctx, s.db, from, to)
}

// AllStops reads the current stop inventory.
func (s *Service) AllStops(ctx context.Context) []Stop {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	v, err := s.reporter.AllStopsReport(ctx)
	if err != nil {
		return nil
	}
	return v
}

// StopAnalytics reads per-stop analytics for the past week.
func (s *Service) StopAnalytics(ctx context.Context) []StopAnalyticsRow {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	v, err := s.reporter.repo.StopAnalytics(ctx, 7)
	if err != nil {
		return nil
	}
	return v
}

// ---------------------------------------------------------------------------
// Data access helpers
// ---------------------------------------------------------------------------

// RouteInfo looks up a route, using the testability hook if set.
func (s *Service) RouteInfo(ctx context.Context, routeID string) (*RouteInfo, error) {
	if s.getRoute != nil {
		return s.getRoute(ctx, routeID)
	}
	return s.reporter.repo.GetRoute(ctx, routeID)
}

// RouteTimepointSchedule loads timepoint schedule data, using the hook if set.
func (s *Service) RouteTimepointSchedule(ctx context.Context, routeID string, date time.Time) ([]TimepointSchedule, error) {
	if s.routeTimepointLoader != nil {
		return s.routeTimepointLoader(ctx, routeID, date)
	}
	return RouteTimepointSchedule(ctx, s.reporter.db, routeID, date)
}

// RouteServiceDays returns which days in the week of refDate had service for
// routeID. "Had service" means we saw evidence the route was in operation that
// day — either we recorded a stop delay (at least one trip ran) or we recorded
// a cancellation (at least one trip was scheduled, even if it didn't run).
//
// Deliberately does NOT consult transit_calendar_dates: a long-lived prod DB
// can drift past the GTFS bundle's calendar coverage and suddenly every day
// looks like "no service." Observed data is the authority for past and
// current days. Future days never need this function because the week picker
// template disables them via the d.IsFuture check regardless.
func (s *Service) RouteServiceDays(ctx context.Context, routeID string, refDate time.Time) map[string]bool {
	offset := int(refDate.Weekday()) - 1
	if offset < 0 {
		offset = 6
	}
	monday := refDate.AddDate(0, 0, -offset)
	sunday := monday.AddDate(0, 0, 6)

	rows, err := s.db.Query(ctx, `
		SELECT day::date::text
		FROM generate_series($2::date::timestamp, $3::date::timestamp, interval '1 day') AS days(day)
		WHERE EXISTS (
			SELECT 1 FROM transit.stop_delay d
			WHERE d.route_id = $1 AND d.date = day::date
		) OR EXISTS (
			SELECT 1 FROM transit.cancellation c
			WHERE c.route_id = $1 AND c.start_date = TO_CHAR(day, 'YYYYMMDD')
		)
	`, routeID, monday, sunday)
	if err != nil {
		return nil
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var iso string
		if err := rows.Scan(&iso); err == nil {
			result[iso] = true
		}
	}
	return result
}

// RouteCancelDays returns cancellation counts per day in the week of refDate for routeID.
func (s *Service) RouteCancelDays(ctx context.Context, routeID string, refDate time.Time) map[string]int {
	offset := int(refDate.Weekday()) - 1
	if offset < 0 {
		offset = 6
	}
	monday := refDate.AddDate(0, 0, -offset)
	sunday := monday.AddDate(0, 0, 6)

	rows, err := s.db.Query(ctx, `
		SELECT start_date, COUNT(DISTINCT trip_id)
		FROM transit.cancellation
		WHERE route_id = $1
			AND start_date >= $2 AND start_date <= $3
		GROUP BY start_date
	`, routeID, monday.Format("20060102"), sunday.Format("20060102"))
	if err != nil {
		return nil
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var sd string
		var count int
		if err := rows.Scan(&sd, &count); err == nil && len(sd) == 8 {
			result[sd[:4]+"-"+sd[4:6]+"-"+sd[6:8]] = count
		}
	}
	return result
}

// RouteTrackingStats returns total trip observations and first observation date for a route.
// Group scalar keys so PostgreSQL can use a covering index or hash aggregate
// instead of sorting composite records. Keep all stops: some trips are only
// observed downstream.
func (s *Service) RouteTrackingStats(ctx context.Context, routeID string) (totalTrips int, since string) {
	s.reporter.db.QueryRow(ctx, `
		SELECT COUNT(*)::INT, COALESCE(MIN(date)::TEXT, '')
		FROM (
			SELECT date, trip_id
			FROM transit.stop_delay
			WHERE route_id = $1
			GROUP BY date, trip_id
		) trips
	`, routeID).Scan(&totalTrips, &since)
	return
}

// ---------------------------------------------------------------------------
// Reporter delegation
// ---------------------------------------------------------------------------

// StopPredictions returns arrival predictions for a stop. Prefers the
// recorder's in-memory trip feed (refreshed every ~60s by the trips
// poller) so the hot path avoids a synchronous upstream HTTP fetch.
// Falls back to a direct client fetch only on cold boot, before the
// recorder has completed its first poll.
func (s *Service) StopPredictions(ctx context.Context, stopID string) (StopPredictionsResponse, error) {
	feed := s.recorder.LastTripFeed()
	if feed == nil {
		return s.reporter.StopPredictionsReport(ctx, stopID)
	}
	resp, err := StopPredictionsFromFeed(ctx, s.reporter.db, feed, stopID, Now())
	if err != nil {
		return StopPredictionsResponse{}, err
	}
	if resp.Predictions == nil {
		resp.Predictions = []StopPrediction{}
	}
	return resp, nil
}

// TripPlan runs a depart-at trip plan.
func (s *Service) TripPlan(ctx context.Context, origin, dest LatLng, fromStop, toStop string, departSec int, date time.Time) (*PlanResult, error) {
	return s.reporter.TripPlan(ctx, origin, dest, fromStop, toStop, departSec, date)
}

// TripPlanArriveBy runs an arrive-by trip plan.
func (s *Service) TripPlanArriveBy(ctx context.Context, origin, dest LatLng, fromStop, toStop string, arriveSec int, date time.Time) (*PlanResult, error) {
	return s.reporter.TripPlanArriveBy(ctx, origin, dest, fromStop, toStop, arriveSec, date)
}

// NearbyStops returns stops near a lat/lon.
func (s *Service) NearbyStops(ctx context.Context, lat, lon float64, limit int) ([]StopWithDistance, error) {
	return s.reporter.NearestStopsReport(ctx, lat, lon, limit)
}

// VehicleDistance returns the distance of a vehicle from a stop.
func (s *Service) VehicleDistance(ctx context.Context, vehicleID, stopID string) (*VehicleDistance, error) {
	return s.reporter.VehicleDistanceReport(ctx, vehicleID, stopID)
}

// MapTimepointStop is one entry in the timepoints-for-map API response.
type MapTimepointStop struct {
	StopID string   `json:"stop_id"`
	Routes []string `json:"routes"`
	Colors []string `json:"colors"`
}

// TimepointStops returns timepoint stops grouped by stop_id with route colors for map rendering.
func (s *Service) TimepointStops(ctx context.Context) ([]MapTimepointStop, error) {
	return s.reporter.repo.TimepointStops(ctx)
}
