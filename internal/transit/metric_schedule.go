package transit

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MetricStop retains the schedule and geography used to interpret an event.
// It never joins to the replaceable current GTFS tables during a rebuild.
type MetricStop struct {
	ID        string  `json:"id"`
	Sequence  int     `json:"sequence"`
	Arrival   int     `json:"arrival"`
	Departure int     `json:"departure"`
	Timepoint bool    `json:"timepoint"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
}

type MetricTrip struct {
	ID, RouteID, ServiceID    string
	Direction, FirstDeparture int
	Stops                     []MetricStop
}

// ImportMetricSchedule archives a local GTFS bundle without replacing the live
// timetable. Its content hash makes repeated imports cheap and idempotent.
// publishedAt should be the capture/commit time for an archive, not today's date.
func ImportMetricSchedule(ctx context.Context, db *pgxpool.Pool, dir, source string, publishedAt time.Time) (string, error) {
	files := []string{"trips.txt", "stops.txt", "stop_times.txt", "calendar.txt", "calendar_dates.txt"}
	hash := sha256.New()
	data := make(map[string][]map[string]string)
	for _, name := range files {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) && strings.HasPrefix(name, "calendar") {
			continue
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintln(hash, name)
		hash.Write(b)
		data[name], err = readCSV(path)
		if err != nil {
			return "", err
		}
	}
	id := fmt.Sprintf("%x", hash.Sum(nil))
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM transit.metric_schedule WHERE id=$1)`, id).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return id, nil
	}
	stops := map[string]MetricStop{}
	for _, r := range data["stops.txt"] {
		lat, e1 := strconv.ParseFloat(r["stop_lat"], 64)
		lon, e2 := strconv.ParseFloat(r["stop_lon"], 64)
		if e1 != nil || e2 != nil {
			return "", fmt.Errorf("invalid coordinates for stop %s", r["stop_id"])
		}
		stops[r["stop_id"]] = MetricStop{ID: r["stop_id"], Lat: lat, Lon: lon}
	}
	trips := map[string]*MetricTrip{}
	for _, r := range data["trips.txt"] {
		direction, _ := strconv.Atoi(r["direction_id"])
		trips[r["trip_id"]] = &MetricTrip{ID: r["trip_id"], RouteID: r["route_id"], ServiceID: r["service_id"], Direction: direction}
	}
	for _, r := range data["stop_times.txt"] {
		t := trips[r["trip_id"]]
		s, ok := stops[r["stop_id"]]
		if t == nil || !ok {
			return "", fmt.Errorf("unmatched trip/stop in schedule: %s/%s", r["trip_id"], r["stop_id"])
		}
		var err error
		s.Sequence, err = strconv.Atoi(r["stop_sequence"])
		if err != nil {
			return "", err
		}
		s.Arrival, err = metricTime(r["arrival_time"])
		if err != nil {
			return "", err
		}
		s.Departure, err = metricTime(r["departure_time"])
		if err != nil {
			return "", err
		}
		s.Timepoint = r["timepoint"] != "0" // GTFS omission means exact time.
		t.Stops = append(t.Stops, s)
	}
	days := map[string]map[string]bool{}
	setDay := func(service, date string, active bool) {
		if days[service] == nil {
			days[service] = map[string]bool{}
		}
		days[service][date] = active
	}
	for _, r := range data["calendar.txt"] {
		start, e1 := time.ParseInLocation("20060102", r["start_date"], TZ)
		end, e2 := time.ParseInLocation("20060102", r["end_date"], TZ)
		if e1 != nil || e2 != nil || end.Before(start) {
			return "", fmt.Errorf("invalid calendar range")
		}
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			if r[strings.ToLower(d.Weekday().String())] == "1" {
				setDay(r["service_id"], d.Format("2006-01-02"), true)
			}
		}
	}
	for _, r := range data["calendar_dates.txt"] {
		d, err := time.ParseInLocation("20060102", r["date"], TZ)
		if err != nil {
			return "", err
		}
		setDay(r["service_id"], d.Format("2006-01-02"), r["exception_type"] == "1")
	}
	if len(days) == 0 || len(trips) == 0 {
		return "", fmt.Errorf("schedule has no services or trips")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Serializes simultaneous imports of this exact bundle.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, id); err != nil {
		return "", err
	}
	result, err := tx.Exec(ctx, `INSERT INTO transit.metric_schedule(id,source,published_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, source, publishedAt)
	if err != nil {
		return "", err
	}
	if result.RowsAffected() == 0 {
		return id, tx.Commit(ctx)
	}
	var tripRows, serviceRows [][]any
	for _, t := range trips {
		if len(t.Stops) == 0 {
			return "", fmt.Errorf("trip %s has no stops", t.ID)
		}
		sort.Slice(t.Stops, func(i, j int) bool { return t.Stops[i].Sequence < t.Stops[j].Sequence })
		payload, err := json.Marshal(t.Stops)
		if err != nil {
			return "", err
		}
		tripRows = append(tripRows, []any{id, t.ID, t.RouteID, t.ServiceID, t.Direction, t.Stops[0].Departure, payload})
	}
	for service, dates := range days {
		for date, active := range dates {
			if active {
				d, err := time.Parse("2006-01-02", date)
				if err != nil {
					return "", err
				}
				serviceRows = append(serviceRows, []any{id, service, d})
			}
		}
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"transit", "metric_trip"}, []string{"schedule_id", "trip_id", "route_id", "service_id", "direction_id", "first_departure", "stops"}, pgx.CopyFromRows(tripRows)); err != nil {
		return "", err
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"transit", "metric_service"}, []string{"schedule_id", "service_id", "date"}, pgx.CopyFromRows(serviceRows)); err != nil {
		return "", err
	}
	// A new timetable may revise the interpretation of dates already built.
	// Keep their old counts for audit but stop presenting them as current.
	if _, err = tx.Exec(ctx, `UPDATE transit.route_band_chunk c SET metric_version=0
        WHERE EXISTS(SELECT 1 FROM transit.metric_service s WHERE s.schedule_id=$1 AND s.date=c.date)`, id); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM transit.metric_rebuild b
        WHERE EXISTS(SELECT 1 FROM transit.metric_service s WHERE s.schedule_id=$1 AND s.date=b.date)`, id); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func metricTime(value string) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid GTFS time %q", value)
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	s, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil || h < 0 || m < 0 || m > 59 || s < 0 || s > 59 {
		return 0, fmt.Errorf("invalid GTFS time %q", value)
	}
	return h*3600 + m*60 + s, nil
}

func metricScheduleForDate(ctx context.Context, db *pgxpool.Pool, date time.Time) (string, []MetricTrip, error) {
	var id string
	// Prefer a bundle already published on the day. An archive captured later
	// can cover earlier dates, but remains explicitly identified in rebuild logs.
	err := db.QueryRow(ctx, `WITH observed AS MATERIALIZED (
            SELECT DISTINCT trip_id FROM transit.stop_delay WHERE date=$1::date
            UNION SELECT trip_id FROM transit.cancellation WHERE start_date=to_char($1::date,'YYYYMMDD')
        ), matches AS (
            SELECT t.schedule_id,count(*) n FROM transit.metric_trip t
            JOIN transit.metric_service s USING(schedule_id,service_id)
            JOIN observed o USING(trip_id) WHERE s.date=$1::date GROUP BY t.schedule_id
        )
        SELECT m.id FROM transit.metric_schedule m LEFT JOIN matches ON matches.schedule_id=m.id
		WHERE EXISTS(SELECT 1 FROM transit.metric_service s WHERE s.schedule_id=m.id AND s.date=$1::date)
		ORDER BY COALESCE(matches.n,0) DESC, (m.published_at < $1::date + interval '1 day') DESC,
		CASE WHEN m.published_at < $1::date + interval '1 day' THEN m.published_at END DESC,
		m.published_at ASC, m.id LIMIT 1`, date).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	rows, err := db.Query(ctx, `SELECT t.trip_id,t.route_id,t.service_id,t.direction_id,t.first_departure,t.stops
		FROM transit.metric_trip t JOIN transit.metric_service s USING(schedule_id,service_id)
		WHERE t.schedule_id=$1 AND s.date=$2::date ORDER BY t.trip_id`, id, date)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var trips []MetricTrip
	for rows.Next() {
		var t MetricTrip
		var payload []byte
		if err := rows.Scan(&t.ID, &t.RouteID, &t.ServiceID, &t.Direction, &t.FirstDeparture, &payload); err != nil {
			return "", nil, err
		}
		if err := json.Unmarshal(payload, &t.Stops); err != nil {
			return "", nil, err
		}
		trips = append(trips, t)
	}
	return id, trips, rows.Err()
}
