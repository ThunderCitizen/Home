package transitarchive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The opt-in URL needs CREATE DATABASE: all fixtures live in a disposable DB.
func TestPostgresRoundTrip(t *testing.T) {
	url := os.Getenv("TRANSIT_ARCHIVE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TRANSIT_ARCHIVE_TEST_DATABASE_URL for a disposable PostgreSQL round trip")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("transit_archive_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("clean up disposable DB: %v", err)
		}
	}()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `CREATE TABLE schema_migrations(version bigint,dirty boolean);INSERT INTO schema_migrations VALUES(42,false);
 CREATE SCHEMA transit;CREATE SCHEMA gtfs;
 CREATE TABLE transit.vehicle_position(id bigint GENERATED ALWAYS AS IDENTITY,feed_timestamp timestamptz, latitude float8, longitude float8, trip_start_date text, detail jsonb);
 CREATE TABLE transit.metric_schedule(id text,source text,published_at timestamptz);
 CREATE TABLE transit.stop_visit(observed_at timestamptz, service_date date GENERATED ALWAYS AS (((observed_at AT TIME ZONE 'America/Thunder_Bay')-interval '4 hours')::date) STORED);
 CREATE TABLE gtfs.routes(route_id text, names text[]);
 CREATE TABLE gtfs.nullable(value text); INSERT INTO gtfs.nullable VALUES(NULL),(''),(E'\\\\N');
 INSERT INTO transit.vehicle_position(feed_timestamp,latitude,longitude,trip_start_date,detail) VALUES('2020-09-15 12:00Z',48.42,-89.23,'20200915','{"a":1}'),('2020-11-01 12:00Z',48,-89,NULL,NULL);
 INSERT INTO transit.metric_schedule VALUES('hash',E'line one\nline two','2020-09-01 00:00Z');
 INSERT INTO transit.stop_visit(observed_at) VALUES('2020-09-15 06:00Z');
 INSERT INTO gtfs.routes VALUES('1',ARRAY['quoted "name"',E'multi\nline']);`)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "month")
	m, err := Export(ctx, conn, dir, "2020-09", 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tables) != 5 {
		t.Fatal(m.Tables)
	}
	if err = Restore(ctx, conn, dir, "transit_archive_roundtrip"); err != nil {
		t.Fatal(err)
	}
	var equal bool
	err = conn.QueryRow(ctx, `SELECT (SELECT jsonb_agg(to_jsonb(t)) FROM (SELECT * FROM transit.vehicle_position WHERE id=1) t) = (SELECT jsonb_agg(to_jsonb(t)) FROM transit_archive_roundtrip.transit__vehicle_position t)`).Scan(&equal)
	if err != nil || !equal {
		t.Fatalf("GPS source IDs/fields differ: %v %v", equal, err)
	}
	for _, table := range []struct{ schema, name string }{{"transit", "metric_schedule"}, {"transit", "stop_visit"}, {"gtfs", "routes"}, {"gtfs", "nullable"}} {
		query := fmt.Sprintf("SELECT (SELECT jsonb_agg(to_jsonb(t)) FROM %s t) = (SELECT jsonb_agg(to_jsonb(t)) FROM %s t)", pgx.Identifier{table.schema, table.name}.Sanitize(), pgx.Identifier{"transit_archive_roundtrip", table.schema + "__" + table.name}.Sanitize())
		if err = conn.QueryRow(ctx, query).Scan(&equal); err != nil || !equal {
			t.Fatalf("%s differs: %v %v", table.name, equal, err)
		}
	}
	if err = Restore(ctx, conn, dir, "transit_archive_roundtrip"); err == nil {
		t.Fatal("restore overwrote existing schema")
	}
}
