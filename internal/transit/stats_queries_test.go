package transit

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func float32Ptr(value float32) *float32 { return &value }

func TestStatsQueriesDatabase(t *testing.T) {
	db := routeQueryTestDB(t)
	ctx := t.Context()
	repo := NewRepo(db)
	svc := NewService(db, nil)

	if buckets, err := repo.DayPercentiles(ctx); err != nil || buckets != nil {
		t.Fatalf("empty percentiles = %+v, %v; want nil, nil", buckets, err)
	}
	if days, err := repo.WeekSummary(ctx); err != nil || days != nil {
		t.Fatalf("empty week = %+v, %v; want nil, nil", days, err)
	}
	if report := svc.Stats(ctx, "percentiles"); report == nil || report.Type != "percentiles" || report.Buckets == nil || len(report.Buckets) != 0 {
		t.Fatalf("empty percentile report = %+v", report)
	}
	if report := svc.Stats(ctx, "week"); report == nil || report.Type != "week" || report.Days == nil || len(report.Days) != 0 {
		t.Fatalf("empty week report = %+v", report)
	}

	// Stay away from rolling-window boundaries. Future observations must be excluded.
	var base time.Time
	if err := db.QueryRow(ctx, `SELECT date_trunc('hour', now()) - interval '2 hours'`).Scan(&base); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO transit.stop_delay
			(date, trip_id, route_id, stop_id, arrival_delay, departure_delay, last_updated)
		SELECT ($1::timestamptz + offset_at)::date, trip_id, 'stats', 'stop', arrival, departure,
			$1::timestamptz + offset_at
		FROM (VALUES
			('early', interval '0 minutes', -120, 999),
			('on-time', interval '1 minute', 0, 777),
			('boundary', interval '2 minutes', 60, 30),
			('departure', interval '3 minutes', NULL, 120),
			('unknown', interval '4 minutes', NULL, NULL),
            ('fifth', interval '5 minutes', 0, 0),
			('small-1', interval '30 minutes', 0, NULL),
			('small-2', interval '31 minutes', 0, NULL),
			('small-3', interval '32 minutes', 0, NULL),
			('small-4', interval '33 minutes', 0, NULL),
			('week-only', interval '-48 hours', 300, NULL),
			('expired', interval '-8 days', 0, NULL),
			('future-1', interval '48 hours', -30, NULL),
			('future-2', interval '48 hours 1 minute', -30, NULL),
			('future-3', interval '48 hours 2 minutes', -30, NULL),
			('future-4', interval '48 hours 3 minutes', -30, NULL),
			('future-5', interval '48 hours 4 minutes', -30, NULL)
		) f(trip_id, offset_at, arrival, departure)
	`, base); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO transit.cancellation (trip_id, route_id, feed_timestamp)
		SELECT trip_id, 'stats', $1::timestamptz + offset_at
		FROM (VALUES
			('repeated', interval '0 minutes'), ('repeated', interval '1 minute'),
			('other', interval '2 minutes'), ('cancellation-only', interval '-24 hours'),
			('future', interval '48 hours'), ('expired', interval '-8 days')
		) f(trip_id, offset_at)
	`, base); err != nil {
		t.Fatal(err)
	}

	assertBucket := func(got DelayPercentileBucket, at time.Time, count int, percentiles [4]float64) {
		t.Helper()
		if !got.BucketTime.Equal(at) || got.Count != count {
			t.Errorf("bucket = %+v; want time %v, count %d", got, at, count)
		}
		for i, value := range [4]float64{got.P50, got.P90, got.P99, got.P999} {
			if math.Abs(value-percentiles[i]) > 0.000001 {
				t.Errorf("percentile %d = %v, want %v", i, value, percentiles[i])
			}
		}
	}
	assertDay := func(got DaySummary, at time.Time, onTime, avgDelay float32, cancellations int) {
		t.Helper()
		if got.Date.Format(time.DateOnly) != at.In(TZ).Format(time.DateOnly) ||
			got.AvgOnTime == nil || got.AvgDelay == nil || math.Abs(float64(*got.AvgOnTime-onTime)) > 0.00001 || *got.AvgDelay != avgDelay || got.Cancellations != cancellations {
			t.Errorf("day = %+v; want date %s, on-time %v, delay %v, cancellations %d",
				got, at.In(TZ).Format(time.DateOnly), onTime, avgDelay, cancellations)
		}
	}

	percentiles := svc.Stats(ctx, "percentiles")
	if percentiles == nil || len(percentiles.Buckets) != 1 {
		t.Fatalf("percentile report = %+v; want one bucket", percentiles)
	}
	assertBucket(percentiles.Buckets[0], base, 5, [4]float64{120, 910.2, 990.12, 998.112})
	week := svc.Stats(ctx, "week")
	if week == nil || len(week.Days) != 2 {
		t.Fatalf("week report = %+v; want two days", week)
	}
	assertDay(week.Days[0], base.Add(-48*time.Hour), 100, 300, 0)
	assertDay(week.Days[1], base, 100*float32(7)/9, 214, 2)

	// The complete migration is covered by TestIndexCleanupMigrationsDatabase.
	// This focused fixture only needs the selective access path for report parity.
	routeQueryExec(t, db, `CREATE INDEX idx_transit_stop_delay_updated ON transit.stop_delay (last_updated)`)
	var valid bool
	var columns int
	if err := db.QueryRow(ctx, `
		SELECT indisvalid, indnatts FROM pg_index
		WHERE indexrelid = 'transit.idx_transit_stop_delay_updated'::regclass
	`).Scan(&valid, &columns); err != nil || !valid || columns != 1 {
		t.Fatalf("index valid=%v columns=%d: %v", valid, columns, err)
	}
	if got := svc.Stats(ctx, "percentiles"); !reflect.DeepEqual(got, percentiles) {
		t.Errorf("percentiles changed after indexing: %+v, want %+v", got, percentiles)
	}
	if got := svc.Stats(ctx, "week"); !reflect.DeepEqual(got, week) {
		t.Errorf("week changed after indexing: %+v, want %+v", got, week)
	}
	routeQueryExec(t, db, `DROP INDEX transit.idx_transit_stop_delay_updated`)
	var removed bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('transit.idx_transit_stop_delay_updated') IS NULL`).Scan(&removed); err != nil || !removed {
		t.Fatalf("down migration removed index=%v: %v", removed, err)
	}

	// A service instance must immediately reflect edits without invalidation or
	// a warm-up cycle. Unknown delays never enter the denominator.
	routeQueryExec(t, db, `UPDATE transit.stop_delay SET departure_delay = 240 WHERE trip_id = 'departure'`)
	if _, err := db.Exec(ctx, `
		INSERT INTO transit.stop_delay (date, trip_id, route_id, stop_id, arrival_delay, last_updated)
		VALUES ($1::timestamptz::date, 'added', 'stats', 'stop', 0, $1::timestamptz + interval '5 minutes')
	`, base); err != nil {
		t.Fatal(err)
	}
	updatedPercentiles := svc.Stats(ctx, "percentiles")
	if updatedPercentiles == nil || len(updatedPercentiles.Buckets) != 1 {
		t.Fatalf("updated percentiles = %+v", updatedPercentiles)
	}
	assertBucket(updatedPercentiles.Buckets[0], base, 6, [4]float64{135, 888, 987.9, 997.89})
	updatedWeek := svc.Stats(ctx, "week")
	if updatedWeek == nil || len(updatedWeek.Days) != 2 {
		t.Fatalf("updated week = %+v", updatedWeek)
	}
	assertDay(updatedWeek.Days[1], base, 80, 204.6, 2)

	// All-NULL observations are unavailable, not zero or a server error.
	routeQueryExec(t, db, `TRUNCATE transit.stop_delay, transit.cancellation`)
	if _, err := db.Exec(ctx, `
		INSERT INTO transit.stop_delay (date, trip_id, stop_id, last_updated)
		SELECT $1::timestamptz::date, 'null-' || i, 'stop', $1 FROM generate_series(1, 5) i
	`, base); err != nil {
		t.Fatal(err)
	}
	if buckets, err := repo.DayPercentiles(ctx); err != nil || len(buckets) != 0 {
		t.Fatalf("all-NULL percentiles = %+v, %v; want no buckets and no error", buckets, err)
	}
	if report := svc.Stats(ctx, "percentiles"); report == nil || len(report.Buckets) != 0 {
		t.Fatalf("unknown-only percentile report: %+v", report)
	}
	if days, err := repo.WeekSummary(ctx); err != nil || len(days) != 0 {
		t.Fatalf("all-NULL week = %+v, %v", days, err)
	}
}
