package transit

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func snapshotsAt(t *testing.T, db *pgxpool.Pool, now time.Time) []TransitSnapshot {
	t.Helper()
	// Only the test clock changes; production still uses PostgreSQL NOW().
	query := strings.ReplaceAll(daySnapshotsQuery, "NOW()", "$1::timestamptz")
	rows, err := db.Query(t.Context(), query, now)
	if err != nil {
		t.Fatal(err)
	}
	result, err := pgx.CollectRows(rows, pgx.RowToStructByPos[TransitSnapshot])
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDaySnapshotsDatabase(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `
		CREATE TABLE transit.vehicle_position (feed_timestamp timestamptz, vehicle_id text, route_id text);
		CREATE TABLE transit.alert (feed_timestamp timestamptz, alert_id text);
	`)
	clear := func() {
		routeQueryExec(t, db, `TRUNCATE transit.vehicle_position, transit.stop_delay, transit.alert, transit.cancellation`)
	}
	execAt := func(t *testing.T, now time.Time, query string) {
		t.Helper()
		if _, err := db.Exec(t.Context(), query, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ name, now string }{
		{"midnight", "2026-09-10T00:02:45-04:00"},
		{"spring daylight saving", "2026-03-09T00:02:45-04:00"},
		{"fall daylight saving", "2026-11-02T00:02:45-05:00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clear()
			now, err := time.Parse(time.RFC3339, test.now)
			if err != nil {
				t.Fatal(err)
			}
			execAt(t, now, `INSERT INTO transit.vehicle_position VALUES
				($1::timestamptz - interval '24 hours 1 microsecond', 'too-old', 'old'),
				($1::timestamptz - interval '24 hours', 'edge', 'A'),
				($1::timestamptz - interval '23 hours 59 minutes', 'edge', 'B'),
				($1::timestamptz - interval '23 hours', 'next-hour', 'A'),
				($1::timestamptz - interval '12 hours', 'v1', 'A'),
				($1::timestamptz - interval '12 hours', 'v1', 'B'),
				($1::timestamptz - interval '12 hours', 'v2', NULL),
				($1::timestamptz - interval '12 hours', NULL, 'C'),
				($1::timestamptz + interval '1 minute', 'within-current-bucket', 'A'),
				($1::timestamptz + interval '3 minutes', 'beyond-grid', 'A')`)
			execAt(t, now, `INSERT INTO transit.vehicle_position
				SELECT $1::timestamptz - interval '12 hours' + n * interval '1 second', 'v1', 'A'
				FROM generate_series(0, 59) n`)
			execAt(t, now, `INSERT INTO transit.stop_delay (date, trip_id, stop_id, route_id, last_updated, arrival_delay, departure_delay)
				SELECT $1::timestamptz::date, id, 'stop', 'A', $1::timestamptz - interval '12 hours', arrival, departure
				FROM (VALUES ('early-boundary', -60, NULL), ('late-boundary', 60, NULL), ('late', 61, NULL),
					('early', -61, NULL), ('fallback', NULL, 30), ('unknown', NULL, NULL)) d(id, arrival, departure)`)
			execAt(t, now, `INSERT INTO transit.stop_delay (date, trip_id, stop_id, route_id, last_updated)
				VALUES ($1::timestamptz::date, 'null-only', 'stop', 'A', $1::timestamptz - interval '6 hours')`)
			execAt(t, now, `INSERT INTO transit.alert
				SELECT $1::timestamptz - interval '12 hours', id FROM (VALUES ('a1'), ('a1'), ('a2'), (NULL)) a(id)
				UNION ALL SELECT $1::timestamptz - interval '3 hours', 'alert-only'`)
			execAt(t, now, `INSERT INTO transit.cancellation (feed_timestamp, trip_id)
				SELECT $1::timestamptz - interval '12 hours', id FROM (VALUES ('c1'), ('c1'), ('c2'), (NULL)) c(id)
				UNION ALL SELECT $1::timestamptz - interval '3 hours', 'cancel-only'`)
			want := []TransitSnapshot{
				{CapturedAt: now.Add(-24 * time.Hour).Truncate(5 * time.Minute), ActiveVehicles: 1, ActiveRoutes: 2},
				{CapturedAt: now.Add(-23 * time.Hour).Truncate(5 * time.Minute), ActiveVehicles: 1, ActiveRoutes: 1},
				{CapturedAt: now.Add(-12 * time.Hour).Truncate(5 * time.Minute), ActiveVehicles: 2, ActiveRoutes: 3,
					OnTimePct: 50, AvgDelaySeconds: 6, LateCount: 1, EarlyCount: 1, MeasurementCount: 6, AlertCount: 2, Cancellations: 2},
				{CapturedAt: now.Add(-6 * time.Hour).Truncate(5 * time.Minute), MeasurementCount: 1},
				{CapturedAt: now.Truncate(5 * time.Minute), ActiveVehicles: 1, ActiveRoutes: 1},
			}
			got := snapshotsAt(t, db, now)
			if len(got) != len(want) {
				t.Fatalf("got %d snapshots, want %d: %+v", len(got), len(want), got)
			}
			for i := range got {
				if !got[i].CapturedAt.Equal(want[i].CapturedAt) {
					t.Errorf("snapshot %d timestamp = %v, want %v", i, got[i].CapturedAt, want[i].CapturedAt)
				}
				got[i].CapturedAt = want[i].CapturedAt // Compare the values without location pointer differences.
				if !reflect.DeepEqual(got[i], want[i]) {
					t.Errorf("snapshot %d = %+v, want %+v", i, got[i], want[i])
				}
			}
			clear()
			execAt(t, now, `INSERT INTO transit.vehicle_position
				SELECT GREATEST(t, $1::timestamptz - interval '24 hours'), 'grid', 'A'
				FROM generate_series(
					date_trunc('minute', $1::timestamptz - interval '24 hours') - interval '2 minutes',
					date_trunc('minute', $1::timestamptz), interval '5 minutes') t`)
			got = snapshotsAt(t, db, now)
			if len(got) != 289 {
				t.Fatalf("complete 24-hour grid contains %d snapshots, want 289", len(got))
			}
			first := now.Add(-24 * time.Hour).Truncate(5 * time.Minute)
			for i, snapshot := range got {
				if !snapshot.CapturedAt.Equal(first.Add(time.Duration(i)*5*time.Minute)) || snapshot.ActiveVehicles != 1 {
					t.Errorf("grid position %d = %+v", i, snapshot)
				}
			}
		})
	}
	t.Run("same service sees fresh events", func(t *testing.T) {
		clear()
		routeQueryExec(t, db, `INSERT INTO transit.vehicle_position VALUES (NOW() - interval '5 minutes', 'first', 'A')`)
		svc := &Service{db: db, reporter: NewReporter(db, nil)}
		first := svc.Stats(t.Context(), "day")
		if first == nil || len(first.Snapshots) != 1 || first.Snapshots[0].ActiveVehicles != 1 {
			t.Fatalf("initial service response = %+v", first)
		}
		routeQueryExec(t, db, `INSERT INTO transit.vehicle_position SELECT feed_timestamp, 'second', 'A' FROM transit.vehicle_position LIMIT 1`)
		second := svc.Stats(t.Context(), "day")
		if second == nil || len(second.Snapshots) != 1 || second.Snapshots[0].ActiveVehicles != 2 {
			t.Errorf("service did not see new vehicle immediately: %+v", second)
		}
	})
}
