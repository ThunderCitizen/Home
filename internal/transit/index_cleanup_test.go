package transit

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIndexCleanupMigrationsDatabase(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `
		ALTER TABLE transit.stop_delay
			ADD COLUMN service_id text,
			ADD COLUMN band text,
			ADD COLUMN is_timepoint boolean DEFAULT false;
		ALTER TABLE transit.cancellation
			ADD COLUMN id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			ADD CONSTRAINT cancellation_trip_id_feed_timestamp_key UNIQUE (trip_id, feed_timestamp);
		ALTER TABLE transit.scheduled_stop ADD COLUMN route_id text;
		CREATE TABLE transit.route_pattern (
			pattern_id text PRIMARY KEY, route_id text NOT NULL,
			headsign text NOT NULL, direction_id integer NOT NULL,
			UNIQUE (route_id, headsign, direction_id)
		);
		CREATE SCHEMA gtfs;
		CREATE TABLE gtfs.calendar_dates (
			service_id text, date date, exception_type integer NOT NULL,
			PRIMARY KEY (service_id, date)
		);
		CREATE TABLE gtfs.stop_times (
			trip_id text, stop_sequence integer, stop_id text NOT NULL,
			PRIMARY KEY (trip_id, stop_sequence)
		);
		INSERT INTO transit.stop_delay
			(date, trip_id, stop_id, route_id, arrival_delay, service_id, band, is_first_stop, is_timepoint)
		VALUES
			('2026-09-09', 'trip', 'first', '3C', 10, 'weekday', 'morning', true, false),
			('2026-09-09', 'trip', 'second', '3C', 20, 'weekday', 'morning', false, true);
		INSERT INTO transit.cancellation (trip_id, route_id, start_date, start_time, feed_timestamp) VALUES
			('trip', '3C', '20260909', '06:00:00', '2026-09-09 06:00-04'),
			('unknown', '3C', NULL, NULL, '2026-09-09 06:00-04');
		INSERT INTO transit.scheduled_stop
			(trip_id, stop_id, stop_sequence, is_timepoint, scheduled_departure, route_id)
		VALUES ('trip', 'first', 1, true, '06:00:00', '3C'),
			('trip', 'second', 2, false, '06:05:00', '3C');
		INSERT INTO transit.route_pattern VALUES ('pattern', '3C', 'Terminal', 0);
		INSERT INTO gtfs.calendar_dates VALUES ('weekday', '2026-09-09', 1);
		INSERT INTO gtfs.stop_times VALUES ('trip', 1, 'first');
	`)

	removedIndexes := []string{
		"transit.idx_transit_stop_delay_route_stop_date",
		"transit.idx_transit_stop_delay_service_date",
		"transit.idx_transit_stop_delay_first_stop_band",
		"transit.idx_transit_stop_delay_timepoint_band",
		"transit.idx_transit_stop_delay_last_updated",
		"transit.idx_transit_cancellation_route_start",
		"transit.idx_transit_route_pattern_route",
		"gtfs.idx_gtfs_calendar_dates_date",
		"gtfs.idx_gtfs_stop_times_stop",
		"transit.idx_transit_scheduled_stop_first_dep",
		"transit.idx_transit_scheduled_stop_tp_dep",
	}
	readMigration := func(file string) string {
		t.Helper()
		contents, err := os.ReadFile(filepath.Join("..", "..", "migrations", file))
		if err != nil {
			t.Fatal(err)
		}
		return string(contents)
	}

	// Build the original indexes from the original schema, independently of
	// the cleanup's down migrations. PostgreSQL canonicalizes both definitions
	// below, so partial predicates, key order, access method and BRIN options
	// must all survive the round trip.
	originalSchema := readMigration("000001_schema.up.sql")
	for _, index := range removedIndexes {
		_, name, _ := strings.Cut(index, ".")
		pattern := regexp.MustCompile(`(?m)^CREATE INDEX ` + regexp.QuoteMeta(name) + ` ON [^;]+;`)
		statements := pattern.FindAllString(originalSchema, -1)
		if len(statements) != 1 {
			t.Fatalf("found %d original definitions for %s, want one", len(statements), index)
		}
		routeQueryExec(t, db, statements[0])
	}
	before := indexCleanupDefinitions(t, db)
	routeQueryExec(t, db, readMigration("000022_transit_performance_indexes.up.sql"))
	after := indexCleanupDefinitions(t, db)
	for _, index := range removedIndexes {
		if _, exists := after[index]; exists {
			t.Fatalf("cleanup left %s behind", index)
		}
	}
	for _, index := range []string{
		"transit.idx_transit_stop_delay_route_date_trip",
		"transit.idx_transit_cancellation_route_date_trip",
		"transit.idx_transit_stop_delay_updated",
	} {
		definition, exists := after[index]
		if !exists || definition.Definition == "" {
			t.Fatalf("performance migration did not create %s", index)
		}
	}

	// The recorder's upsert/deduplication constraints must still work after
	// removing secondary indexes. Also exercise the route-pattern uniqueness
	// constraint whose prefix replaces its removed route-only index.
	routeQueryExec(t, db, `
		INSERT INTO transit.stop_delay (date, trip_id, stop_id, route_id, arrival_delay)
		VALUES ('2026-09-09', 'trip', 'first', '3C', 90)
		ON CONFLICT (date, trip_id, stop_id) DO UPDATE
			SET arrival_delay = EXCLUDED.arrival_delay;
		INSERT INTO transit.cancellation (trip_id, route_id, feed_timestamp)
		VALUES ('trip', '3C', '2026-09-09 06:00-04')
		ON CONFLICT (trip_id, feed_timestamp) DO NOTHING;
		INSERT INTO transit.route_pattern VALUES ('other-pattern', '3C', 'Terminal', 0)
		ON CONFLICT (route_id, headsign, direction_id) DO NOTHING;
	`)
	var delay, observations, cancellations, patterns int
	if err := db.QueryRow(t.Context(), `
		SELECT
			(SELECT arrival_delay FROM transit.stop_delay WHERE trip_id = 'trip' AND stop_id = 'first'),
			(SELECT COUNT(*) FROM transit.stop_delay),
			(SELECT COUNT(*) FROM transit.cancellation),
			(SELECT COUNT(*) FROM transit.route_pattern)
	`).Scan(&delay, &observations, &cancellations, &patterns); err != nil {
		t.Fatal(err)
	}
	if delay != 90 || observations != 2 || cancellations != 2 || patterns != 1 {
		t.Fatalf("conflict results: delay=%d observations=%d cancellations=%d patterns=%d", delay, observations, cancellations, patterns)
	}

	routeQueryExec(t, db, readMigration("000022_transit_performance_indexes.down.sql"))
	if got := indexCleanupDefinitions(t, db); !reflect.DeepEqual(got, before) {
		t.Fatalf("indexes after rollback = %#v; want original definitions %#v", got, before)
	}
}

type cleanupIndexDefinition struct {
	Definition string
	Constraint string
	Primary    bool
	Unique     bool
}

func indexCleanupDefinitions(t *testing.T, db *pgxpool.Pool) map[string]cleanupIndexDefinition {
	t.Helper()
	rows, err := db.Query(t.Context(), `
		SELECT n.nspname || '.' || c.relname, pg_get_indexdef(c.oid),
			COALESCE(con.conname || ': ' || pg_get_constraintdef(con.oid), ''),
			i.indisprimary, i.indisunique, i.indisvalid, i.indisready
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_constraint con ON con.conindid = c.oid AND con.contype IN ('p', 'u')
		WHERE n.nspname IN ('transit', 'gtfs')
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make(map[string]cleanupIndexDefinition)
	for rows.Next() {
		var name string
		var definition cleanupIndexDefinition
		var valid, ready bool
		if err := rows.Scan(&name, &definition.Definition, &definition.Constraint, &definition.Primary, &definition.Unique, &valid, &ready); err != nil {
			t.Fatal(err)
		}
		if !valid || !ready {
			t.Fatalf("index %s is invalid or not ready: valid=%v ready=%v", name, valid, ready)
		}
		result[name] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
