package transit

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/transit/chunk"
)

type passageFix struct {
	ID                          int64
	Trip, Vehicle, Stop, Status string
	At                          time.Time
	Lat, Lon                    float64
}
type metricPassage struct {
	Trip, Stop       string
	At               time.Time
	SourceA, SourceB int64
	Distance         float64
	ambiguous        bool
	last             time.Time
}

// passageBetween requires a short, plausible segment and corroboration from
// the feed's stop progression. A route passing near another stop is not enough.
// The thresholds are explicit screening choices, not accuracy guarantees.
func passageBetween(a, b passageFix, t MetricTrip) []metricPassage {
	elapsed := b.At.Sub(a.At).Seconds()
	if a.Trip != b.Trip || a.Vehicle != b.Vehicle || elapsed <= 0 || elapsed > 30 {
		return nil
	}
	if haversineMeters(a.Lat, a.Lon, b.Lat, b.Lon)/elapsed > 35 {
		return nil
	}
	var out []metricPassage
	for i, s := range t.Stops {
		if !s.Timepoint || i == 0 || i == len(t.Stops)-1 || isMetricTerminal(s.ID) {
			continue
		}
		repeated := false
		for j, other := range t.Stops {
			if i != j && s.ID == other.ID {
				repeated = true
				break
			}
		}
		if repeated {
			continue
		}
		distance, fraction := segmentDistToPoint(a.Lat, a.Lon, b.Lat, b.Lon, s.Lat, s.Lon)
		if distance > 35 {
			continue
		}
		stopped := (a.Stop == s.ID && a.Status == "STOPPED_AT") || (b.Stop == s.ID && b.Status == "STOPPED_AT")
		progressed := a.Stop == s.ID && b.Stop == t.Stops[i+1].ID
		if !stopped && !progressed {
			continue
		}
		out = append(out, metricPassage{Trip: t.ID, Stop: s.ID, At: a.At.Add(time.Duration(fraction * elapsed * float64(time.Second))), SourceA: a.ID, SourceB: b.ID, Distance: distance})
	}
	return out
}

// RebuildMetricPassages reconstructs a single service day from the retained GPS
// log. It only replaces its own derived table, never raw observations. All
// writes commit together, and source row IDs make every estimate inspectable.
func RebuildMetricPassages(ctx context.Context, db *pgxpool.Pool, date time.Time) (int, error) {
	scheduleID, trips, err := metricScheduleForDate(ctx, db, date)
	if err != nil {
		return 0, err
	}
	if len(trips) == 0 {
		return 0, fmt.Errorf("no archived schedule for %s", date.Format("2006-01-02"))
	}
	byTrip := map[string]MetricTrip{}
	for _, t := range trips {
		byTrip[t.ID] = t
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), 4, 0, 0, 0, TZ)
	end := start.AddDate(0, 0, 1)
	rows, err := db.Query(ctx, `SELECT id,trip_id,vehicle_id,COALESCE(current_stop_id,''),COALESCE(stop_status,''),
		COALESCE(measurement_timestamp,feed_timestamp),latitude,longitude
		FROM transit.vehicle_position WHERE feed_timestamp >= $1 AND feed_timestamp < $2
		AND trip_id IS NOT NULL AND vehicle_id IS NOT NULL
		AND (trip_start_date IS NULL OR trip_start_date='' OR trip_start_date=to_char($3::date,'YYYYMMDD'))
		ORDER BY vehicle_id,feed_timestamp,id`, start, end, date)
	if err != nil {
		return 0, err
	}
	previous := map[string]passageFix{}
	passages := map[metricStopKey]metricPassage{}
	vehicles := map[string]string{}
	ambiguousTrips := map[string]bool{}
	for rows.Next() {
		var p passageFix
		if err := rows.Scan(&p.ID, &p.Trip, &p.Vehicle, &p.Stop, &p.Status, &p.At, &p.Lat, &p.Lon); err != nil {
			rows.Close()
			return 0, err
		}
		a, ok := previous[p.Vehicle]
		previous[p.Vehicle] = p
		t, known := byTrip[p.Trip]
		if !known || !ok {
			continue
		}
		if v := vehicles[p.Trip]; v != "" && v != p.Vehicle {
			ambiguousTrips[p.Trip] = true
		}
		vehicles[p.Trip] = p.Vehicle
		for _, candidate := range passageBetween(a, p, t) {
			key := metricStopKey{candidate.Trip, candidate.Stop}
			old, exists := passages[key]
			if exists {
				candidate.ambiguous = old.ambiguous || candidate.At.Sub(old.last) > 90*time.Second
				if old.Distance <= candidate.Distance {
					candidate.At = old.At
					candidate.Distance = old.Distance
					candidate.SourceA = old.SourceA
					candidate.SourceB = old.SourceB
				}
			}
			candidate.last = p.At
			passages[key] = candidate
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	// Reject nonmonotone passages: a bus on a nearby crossing must not create a
	// backwards trip. Exclude the ambiguous trip, not just the inconvenient gap.
	for _, t := range trips {
		last := math.Inf(-1)
		for _, s := range t.Stops {
			if p, ok := passages[metricStopKey{t.ID, s.ID}]; ok && !p.ambiguous {
				v := p.At.Sub(metricTimeOrigin(date)).Seconds()
				if v < last {
					ambiguousTrips[t.ID] = true
				}
				last = v
			}
		}
	}
	var keys []metricStopKey
	for key, p := range passages {
		if !p.ambiguous && !ambiguousTrips[p.Trip] {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].trip != keys[j].trip {
			return keys[i].trip < keys[j].trip
		}
		return keys[i].stop < keys[j].stop
	})
	values := make([][]any, 0, len(keys))
	for _, key := range keys {
		p := passages[key]
		values = append(values, []any{date, scheduleID, chunk.Version, p.Trip, p.Stop, p.At, p.SourceA, p.SourceB})
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(23001,($1::date-date '2000-01-01'))`, date); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM transit.metric_passage WHERE date=$1::date`, date); err != nil {
		return 0, err
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"transit", "metric_passage"}, []string{"date", "schedule_id", "detector_version", "trip_id", "stop_id", "observed_at", "source_a", "source_b"}, pgx.CopyFromRows(values)); err != nil {
		return 0, err
	}
	return len(values), tx.Commit(ctx)
}
