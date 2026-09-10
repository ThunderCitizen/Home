package transit

import (
	"context"
	"crypto/rand"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests need PostgreSQL and permission to create a database. The supplied
// database is only the admin connection; fixtures go into a unique scratch DB.
func routeQueryTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TRANSIT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TRANSIT_TEST_DATABASE_URL to run PostgreSQL route query tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	name := "transit_query_test_" + rand.Text()
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+identifier+" WITH (FORCE)"); err != nil {
			t.Errorf("drop scratch database %s: %v", name, err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	config.ConnConfig.RuntimeParams["timezone"] = "America/Thunder_Bay"
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	// Only columns used by these readers are needed; no PostGIS or source data.
	routeQueryExec(t, db, `
		CREATE SCHEMA transit;
		CREATE TABLE transit.trip_catalog (
			trip_id text PRIMARY KEY, route_id text, service_id text, headsign text,
			scheduled_first_dep_time text, scheduled_last_arr_time text
		);
		CREATE TABLE transit.service_calendar (service_id text, date date, PRIMARY KEY (service_id, date));
		CREATE TABLE transit.stop (stop_id text PRIMARY KEY, name text);
		CREATE TABLE transit.scheduled_stop (
			trip_id text, stop_id text, stop_sequence int, is_timepoint boolean,
			scheduled_arrival text, scheduled_departure text,
			PRIMARY KEY (trip_id, stop_sequence)
		);
		CREATE TABLE transit.stop_delay (
			date date, trip_id text, route_id text, stop_id text, is_first_stop boolean,
			last_updated timestamptz DEFAULT now(),
			arrival_delay int, departure_delay int, PRIMARY KEY (date, trip_id, stop_id)
		);
		CREATE TABLE transit.cancellation (
			trip_id text, route_id text, start_date text, start_time text,
			feed_timestamp timestamptz
		);
		CREATE TABLE transit.route_band_chunk (
			route_id text, date date, band text, service_kind text DEFAULT 'weekday',
			trip_count int DEFAULT 0, on_time_count int DEFAULT 0,
			scheduled_count int DEFAULT 0, cancelled_count int DEFAULT 0, no_notice_count int DEFAULT 0,
			headway_count int DEFAULT 0, headway_sum_sec float8 DEFAULT 0,
			headway_sum_sec_sq float8 DEFAULT 0, sched_headway_sec float8 DEFAULT 0,
			built_at timestamptz DEFAULT now(), PRIMARY KEY (route_id, date, band)
		);
		CREATE INDEX idx_transit_route_band_chunk_date ON transit.route_band_chunk (date);
	`)
	return db
}

func routeQueryExec(t *testing.T, db *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), sql); err != nil {
		t.Fatal(err)
	}
}

func routeQueryTestIndexes(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	// This fixture only has the route-reader tables, so create the two access
	// paths directly. TestIndexCleanupMigrationsDatabase exercises the complete
	// consolidated migration against every affected table.
	routeQueryExec(t, db, `
		CREATE INDEX idx_transit_stop_delay_route_date_trip
			ON transit.stop_delay (route_id, date, trip_id);
		CREATE INDEX idx_transit_cancellation_route_date_trip
			ON transit.cancellation (route_id, start_date, trip_id);
	`)
}

func assertScheduleCancellations(t *testing.T, db *pgxpool.Pool, routeID string, date time.Time, want map[string]bool) {
	t.Helper()
	ctx := t.Context()
	// The original cast defines the behavior being preserved, including the
	// session timezone and cancellation records with missing service dates.
	rows, err := db.Query(ctx, `
		SELECT tc.trip_id, EXISTS (
			SELECT 1 FROM transit.cancellation c
			WHERE c.trip_id = tc.trip_id AND c.feed_timestamp::date = $2::date
		)
		FROM transit.trip_catalog tc WHERE tc.route_id = $1
	`, routeID, date)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	baseline := make(map[string]bool)
	for rows.Next() {
		var tripID string
		var canceled bool
		if err := rows.Scan(&tripID, &canceled); err != nil {
			t.Fatal(err)
		}
		baseline[tripID] = canceled
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline, want) {
		t.Fatalf("original feed-date behavior = %v, want %v", baseline, want)
	}
	check := func(t *testing.T, trips []TimepointTrip) {
		t.Helper()
		got := make(map[string]bool)
		for _, trip := range trips {
			got[trip.TripID] = trip.Canceled
		}
		if len(trips) != len(want) || !reflect.DeepEqual(got, want) {
			t.Errorf("got %d trips with cancellation flags %v, want %v", len(trips), got, want)
		}
	}
	t.Run("trip schedule has no duplicate trips", func(t *testing.T) {
		trips, err := RouteSchedule(ctx, db, routeID, date)
		if err != nil {
			t.Fatal(err)
		}
		var statuses []TimepointTrip
		for _, trip := range trips {
			statuses = append(statuses, TimepointTrip{TripID: trip.TripID, Canceled: trip.Canceled})
		}
		check(t, statuses)
	})
	for name, load := range map[string]func(context.Context, *pgxpool.Pool, string, time.Time) ([]TimepointSchedule, error){
		"public timepoints": RouteTimepointSchedule,
		"audit timepoints":  auditRouteTimepointSchedule,
	} {
		t.Run(name, func(t *testing.T) {
			schedules, err := load(ctx, db, routeID, date)
			if err != nil {
				t.Fatal(err)
			}
			if len(schedules) != 1 || len(schedules[0].Stops) != 2 {
				t.Fatalf("expected one direction with two stops, got %+v", schedules)
			}
			check(t, schedules[0].Trips)
		})
	}
}

func TestRouteQueriesDatabase(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryTestIndexes(t, db)
	ctx := t.Context()
	date := time.Date(2026, time.September, 9, 0, 0, 0, 0, TZ)
	svc := &Service{db: db, reporter: &Reporter{db: db}}

	t.Run("schedule cancellation feed dates", func(t *testing.T) {
		routeQueryExec(t, db, `
			INSERT INTO transit.service_calendar VALUES ('weekday', '2026-09-09');
			INSERT INTO transit.stop VALUES ('first', 'First Stop'), ('last', 'Last Stop');
			INSERT INTO transit.trip_catalog
			SELECT trip_id, 'schedule-test', 'weekday', 'Test Direction', '12:00:00', '12:30:00'
			FROM (VALUES ('duplicates'), ('overnight'), ('other-day'), ('unknown-day'),
				('day-start'), ('last-instant'), ('next-day'), ('before-day')) t(trip_id);
			INSERT INTO transit.scheduled_stop
			SELECT tc.trip_id, s.stop_id, s.seq, true, s.at, s.at
			FROM transit.trip_catalog tc
			CROSS JOIN (VALUES ('first', 1, '12:00:00'), ('last', 2, '12:30:00')) s(stop_id, seq, at);
			INSERT INTO transit.cancellation (trip_id, route_id, start_date, feed_timestamp) VALUES
				('duplicates', 'schedule-test', '20260909', '2026-09-09 12:00:00-04'),
				('duplicates', 'schedule-test', '20260909', '2026-09-09 12:01:00-04'),
				('overnight', 'schedule-test', '20260909', '2026-09-10 01:30:00-04'),
				('other-day', 'schedule-test', '20260910', '2026-09-09 12:00:00-04'),
				('unknown-day', 'schedule-test', NULL, '2026-09-09 12:00:00-04'),
				('day-start', 'schedule-test', '20260909', '2026-09-09 00:00:00-04'),
				('last-instant', 'schedule-test', '20260909', '2026-09-09 23:59:59.999999-04'),
				('next-day', 'schedule-test', '20260909', '2026-09-10 00:00:00-04'),
				('before-day', 'schedule-test', '20260909', '2026-09-08 23:59:59.999999-04');
		`)
		assertScheduleCancellations(t, db, "schedule-test", date, map[string]bool{
			"duplicates": true, "overnight": false, "other-day": true, "unknown-day": true,
			"day-start": true, "last-instant": true, "next-day": false, "before-day": false,
		})
	})

	t.Run("schedule feed dates span daylight saving transitions", func(t *testing.T) {
		routeQueryExec(t, db, `
			INSERT INTO transit.service_calendar VALUES ('dst', '2026-03-08'), ('dst', '2026-11-01');
			INSERT INTO transit.trip_catalog
			SELECT trip_id, 'dst-test', 'dst', 'DST Direction', '12:00:00', '12:30:00'
			FROM (VALUES ('spring-start'), ('spring-late'), ('spring-next'),
				('fall-start'), ('fall-first-0130'), ('fall-second-0130'), ('fall-late'), ('fall-next')) t(trip_id);
			INSERT INTO transit.scheduled_stop
			SELECT tc.trip_id, s.stop_id, s.seq, true, s.at, s.at
			FROM transit.trip_catalog tc
			CROSS JOIN (VALUES ('first', 1, '12:00:00'), ('last', 2, '12:30:00')) s(stop_id, seq, at)
			WHERE tc.route_id = 'dst-test';
			INSERT INTO transit.cancellation (trip_id, route_id, feed_timestamp) VALUES
				('spring-start', 'dst-test', '2026-03-08 00:00:00-05'),
				('spring-late', 'dst-test', '2026-03-08 23:30:00-04'),
				('spring-next', 'dst-test', '2026-03-09 00:00:00-04'),
				('fall-start', 'dst-test', '2026-11-01 00:00:00-04'),
				('fall-first-0130', 'dst-test', '2026-11-01 01:30:00-04'),
				('fall-second-0130', 'dst-test', '2026-11-01 01:30:00-05'),
				('fall-late', 'dst-test', '2026-11-01 23:30:00-05'),
				('fall-next', 'dst-test', '2026-11-02 00:00:00-05');
		`)
		for _, test := range []struct {
			name     string
			date     time.Time
			canceled []string
		}{
			{"23 hour day", time.Date(2026, 3, 8, 0, 0, 0, 0, TZ), []string{"spring-start", "spring-late"}},
			{"25 hour day", time.Date(2026, 11, 1, 0, 0, 0, 0, TZ), []string{"fall-start", "fall-first-0130", "fall-second-0130", "fall-late"}},
		} {
			t.Run(test.name, func(t *testing.T) {
				want := map[string]bool{
					"spring-start": false, "spring-late": false, "spring-next": false,
					"fall-start": false, "fall-first-0130": false, "fall-second-0130": false, "fall-late": false, "fall-next": false,
				}
				for _, tripID := range test.canceled {
					want[tripID] = true
				}
				assertScheduleCancellations(t, db, "dst-test", test.date, want)
			})
		}
	})

	t.Run("tracking counts distinct trips and days", func(t *testing.T) {
		routeQueryExec(t, db, `
			INSERT INTO transit.stop_delay (date, trip_id, route_id, stop_id, is_first_stop) VALUES
				('2026-09-07', 'repeat', 'stats-test', 'first', true),
				('2026-09-07', 'repeat', 'stats-test', 'last', false),
				('2026-09-08', 'repeat', 'stats-test', 'last', false),
				('2026-09-08', 'downstream-only', 'stats-test', 'last', false),
				('2026-09-01', 'another-route', 'other-stats', 'first', true);
		`)
		count, since := svc.RouteTrackingStats(ctx, "stats-test")
		if count != 3 || since != "2026-09-07" {
			t.Errorf("got %d trips since %q, want 3 since 2026-09-07", count, since)
		}
		count, since = svc.RouteTrackingStats(ctx, "missing-route")
		if count != 0 || since != "" {
			t.Errorf("empty route: got %d trips since %q", count, since)
		}
	})

	t.Run("service week includes observed and cancelled only days", func(t *testing.T) {
		routeQueryExec(t, db, `
			INSERT INTO transit.stop_delay (date, trip_id, route_id, stop_id, is_first_stop) VALUES
				('2026-09-06', 'week-trip', 'week-test', 'first', true),
				('2026-09-07', 'week-trip', 'week-test', 'first', true),
				('2026-09-07', 'week-trip', 'week-test', 'last', false),
				('2026-09-10', 'week-trip', 'week-test', 'last', false),
				('2026-09-13', 'week-trip', 'week-test', 'last', false),
				('2026-09-14', 'week-trip', 'week-test', 'first', true),
				('2026-09-09', 'week-trip', 'other-week', 'first', true);
			INSERT INTO transit.cancellation (trip_id, route_id, start_date, feed_timestamp) VALUES
				('week-trip', 'week-test', '20260906', '2026-09-06 12:00:00-04'),
				('week-trip', 'week-test', '20260907', '2026-09-07 12:00:00-04'),
				('week-trip', 'week-test', '20260908', '2026-09-08 12:00:00-04'),
				('week-trip', 'week-test', '20260908', '2026-09-08 12:01:00-04'),
				('week-trip', 'week-test', '20260914', '2026-09-14 12:00:00-04'),
				('unknown-week', 'week-test', NULL, '2026-09-09 12:00:00-04'),
				('other-week', 'other-week', '20260909', '2026-09-09 12:00:00-04');
		`)
		want := map[string]bool{"2026-09-07": true, "2026-09-08": true, "2026-09-10": true, "2026-09-13": true}
		for _, ref := range []time.Time{date, date.AddDate(0, 0, 4)} {
			if got := svc.RouteServiceDays(ctx, "week-test", ref); !reflect.DeepEqual(got, want) {
				t.Errorf("week containing %s: got %v, want %v", ref.Format("2006-01-02"), got, want)
			}
		}
	})

	t.Run("chunk reads reflect new data and corrections", func(t *testing.T) {
		repo := NewRepo(db)
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := repo.EarliestChunkDate(cancelledCtx); err == nil {
			t.Fatal("expected an error for the cancelled lookup")
		}
		if earliest, err := repo.EarliestChunkDate(ctx); err != nil || !earliest.IsZero() {
			t.Fatalf("empty earliest date = %v, %v", earliest, err)
		}
		from := date.AddDate(0, 0, -1)
		if rows, err := repo.Chunks(ctx, from, date); err != nil || len(rows) != 0 {
			t.Fatalf("empty range = %v, %v", rows, err)
		}
		routeQueryExec(t, db, `
			INSERT INTO transit.route_band_chunk (route_id, date, band, trip_count, on_time_count)
			VALUES ('3', '2026-09-09', 'morning', 10, 8);
		`)
		if earliest, err := repo.EarliestChunkDate(ctx); err != nil || earliest.Format("2006-01-02") != "2026-09-09" {
			t.Fatalf("earliest date after first insert = %v, %v", earliest, err)
		}
		if rows, err := repo.Chunks(ctx, from, date); err != nil || len(rows) != 1 || rows[0].Trips != 10 || rows[0].OTPPct != 80 {
			t.Fatalf("new data after empty read = %v, %v", rows, err)
		}
		routeQueryExec(t, db, `
			INSERT INTO transit.route_band_chunk (route_id, date, band) VALUES
				('2', '2026-09-08', 'midday'),
				('1', '2026-09-08', 'evening'),
				('2', '2026-09-08', 'morning'),
				('1', '2026-09-08', 'morning'),
				('1', '2026-09-10', 'morning');
		`)
		if earliest, err := repo.EarliestChunkDate(ctx); err != nil || earliest.Format("2006-01-02") != "2026-09-08" {
			t.Fatalf("earliest date after backfill = %v, %v", earliest, err)
		}
		// Time components must not exclude either boundary day's chunks.
		rows, err := repo.Chunks(ctx, from.Add(15*time.Hour), date.Add(10*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, row := range rows {
			keys = append(keys, row.Date+"/"+row.Band+"/"+row.RouteID)
		}
		want := []string{
			"2026-09-08/morning/1", "2026-09-08/morning/2",
			"2026-09-08/midday/2", "2026-09-08/evening/1",
			"2026-09-09/morning/3",
		}
		if !reflect.DeepEqual(keys, want) {
			t.Errorf("inclusive range order = %v, want %v", keys, want)
		}
		if rows, err := repo.Chunks(ctx, date, from); err != nil || rows != nil {
			t.Errorf("reversed range = %v, %v; want nil, nil", rows, err)
		}
		routeQueryExec(t, db, `
			UPDATE transit.route_band_chunk SET trip_count = 20, on_time_count = 15
			WHERE route_id = '3' AND date = '2026-09-09' AND band = 'morning';
		`)
		if rows, err := repo.Chunks(ctx, date, date); err != nil || len(rows) != 1 || rows[0].Trips != 20 || rows[0].OTPPct != 75 {
			t.Errorf("corrected data = %v, %v", rows, err)
		}
		routeQueryExec(t, db, `DELETE FROM transit.route_band_chunk WHERE date = '2026-09-09'`)
		if rows, err := repo.Chunks(ctx, date, date); err != nil || len(rows) != 0 {
			t.Errorf("deleted data still present = %v, %v", rows, err)
		}
	})
}
