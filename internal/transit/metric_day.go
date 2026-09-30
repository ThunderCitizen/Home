package transit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/transit/chunk"
	"thundercitizen/internal/transit/recipes"
)

type metricStopKey struct{ trip, stop string }
type metricWindowKey struct {
	route, stop, band string
	direction         int
}
type metricWindow struct {
	scheduled, observed []float64
	missing             bool
}

// buildMetricDay reads each event source once for the day. The archived
// timetable supplies the denominator, including routes with no observed trips.
func buildMetricDay(ctx context.Context, db *pgxpool.Pool, date time.Time) (string, []*chunk.Chunk, error) {
	scheduleID, trips, err := metricScheduleForDate(ctx, db, date)
	if err != nil {
		return "", nil, err
	}
	if scheduleID == "" {
		return "", nil, fmt.Errorf("no archived timetable covering %s", date.Format("2006-01-02"))
	}
	delays := map[metricStopKey]int{}
	seenTrips := map[string]bool{}
	rows, err := db.Query(ctx, `SELECT trip_id,stop_id,COALESCE(departure_delay,arrival_delay) FROM transit.stop_delay WHERE date=$1::date`, date)
	if err != nil {
		return "", nil, err
	}
	for rows.Next() {
		var trip, stop string
		var delay *int
		if err := rows.Scan(&trip, &stop, &delay); err != nil {
			rows.Close()
			return "", nil, err
		}
		seenTrips[trip] = true
		if delay != nil {
			delays[metricStopKey{trip, stop}] = *delay
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	cancelled := map[string]time.Time{}
	rows, err = db.Query(ctx, `SELECT trip_id,MIN(feed_timestamp) FROM transit.cancellation WHERE start_date=to_char($1::date,'YYYYMMDD') GROUP BY trip_id`, date)
	if err != nil {
		return "", nil, err
	}
	for rows.Next() {
		var trip string
		var first time.Time
		if err := rows.Scan(&trip, &first); err != nil {
			rows.Close()
			return "", nil, err
		}
		cancelled[trip] = first
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	visits := map[metricStopKey]float64{}
	// Only the screened reconstruction is used for regularity. The old visit
	// detector cannot distinguish route crossings and wrong-direction stops.
	rows, err = db.Query(ctx, `SELECT trip_id,stop_id,observed_at FROM transit.metric_passage
        WHERE date=$1::date AND schedule_id=$2 AND detector_version=$3`, date, scheduleID, chunk.Version)
	if err != nil {
		return "", nil, err
	}
	for rows.Next() {
		var trip, stop string
		var at time.Time
		if err := rows.Scan(&trip, &stop, &at); err != nil {
			rows.Close()
			return "", nil, err
		}
		visits[metricStopKey{trip, stop}] = at.Sub(metricTimeOrigin(date)).Seconds()
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	kind := "weekday"
	if date.Weekday() == time.Saturday {
		kind = "saturday"
	}
	if date.Weekday() == time.Sunday {
		kind = "sunday"
	}
	chunks := map[string]*chunk.Chunk{}
	get := func(route, band string) *chunk.Chunk {
		key := route + "/" + band
		c := chunks[key]
		if c == nil {
			c = &chunk.Chunk{RouteID: route, Date: date, Band: band, ServiceKind: kind, Quality: chunk.Quality{Version: chunk.Version}}
			chunks[key] = c
		}
		return c
	}
	windows := map[metricWindowKey]*metricWindow{}
	for _, t := range trips {
		if band := metricBand(t.FirstDeparture); band != "" {
			c := get(t.RouteID, band)
			c.ScheduledCount++
			if seenTrips[t.ID] {
				c.TripCount++
			}
			if first, ok := cancelled[t.ID]; ok {
				c.CancelledCount++
				if metricTimeOrigin(date).Add(time.Duration(t.FirstDeparture)*time.Second).Sub(first) < 15*time.Minute {
					c.NoNoticeCount++
				}
			}
		}
		counts := map[string]int{}
		for _, s := range t.Stops {
			counts[s.ID]++
		}
		for i, s := range t.Stops {
			if !s.Timepoint || counts[s.ID] != 1 {
				continue
			}
			key := metricStopKey{t.ID, s.ID}
			// Final destinations are not departure punctuality checkpoints.
			if band := metricBand(s.Departure); band != "" && i < len(t.Stops)-1 {
				c := get(t.RouteID, band)
				if delay, ok := delays[key]; ok {
					c.OTPCount++
					switch {
					case delay < chunk.OTPEarlyLimit:
						c.Early++
					case delay > chunk.OTPLateLimit:
						c.Late++
					default:
						c.OTPOnTime++
					}
				}
			}
			// Interior timing points avoid terminal layover/arrival ambiguity.
			if i == 0 || i == len(t.Stops)-1 || isMetricTerminal(s.ID) {
				continue
			}
			band := metricBand(s.Arrival)
			if band == "" {
				continue
			}
			c := get(t.RouteID, band)
			c.ExpectedTimepoints++
			wk := metricWindowKey{t.RouteID, s.ID, band, t.Direction}
			w := windows[wk]
			if w == nil {
				w = &metricWindow{}
				windows[wk] = w
			}
			w.scheduled = append(w.scheduled, float64(s.Arrival))
			if at, ok := visits[key]; ok {
				w.observed = append(w.observed, at)
				c.ObservedTimepoints++
			} else if _, ok := cancelled[t.ID]; !ok {
				w.missing = true
			}
		}
	}
	for key, w := range windows {
		if len(w.scheduled) < 3 {
			continue
		}
		c := get(key.route, key.band)
		c.TotalWindows++
		if w.missing {
			continue
		}
		var start, end float64
		for _, b := range Bands {
			if b.Name == key.band {
				start = float64(b.StartHour * 3600)
				end = float64(b.EndHour * 3600)
			}
		}
		q := recipes.WindowStats(w.scheduled, w.observed, start, end)
		c.EligibleWindows += q.EligibleWindows
		c.WaitObservedArea += q.WaitObservedArea
		c.WaitScheduledArea += q.WaitScheduledArea
		c.WindowSeconds += q.WindowSeconds
		c.CVWeightedSum += q.CVWeightedSum
		c.CVWeight += q.CVWeight
	}
	var out []*chunk.Chunk
	for _, c := range chunks {
		out = append(out, c)
	}
	return scheduleID, out, nil
}

func metricBand(seconds int) string {
	for _, b := range Bands {
		if seconds >= b.StartHour*3600 && seconds < b.EndHour*3600 {
			return b.Name
		}
	}
	return ""
}

func isMetricTerminal(stop string) bool {
	for _, t := range canonicalTerminals {
		if t.StopID == stop {
			return true
		}
	}
	return false
}
