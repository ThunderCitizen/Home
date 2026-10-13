package transitprepare

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func legacyFixture(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv("TRANSIT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TRANSIT_TEST_DATABASE_URL not set")
	}
	ctx := t.Context()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("transit_prepare_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	_, err = conn.Exec(ctx, `CREATE SCHEMA transit;CREATE TABLE schema_migrations(version bigint,dirty bool);INSERT INTO schema_migrations VALUES(22,false);
 CREATE TABLE transit.stop_visit(trip_id text NOT NULL,stop_id text NOT NULL,observed_at timestamptz NOT NULL,distance_m real,PRIMARY KEY(trip_id,stop_id,observed_at));
 INSERT INTO transit.stop_visit VALUES ('trip','stop','2026-09-01 02:00-04',1),('trip','stop','2026-09-01 02:01-04',2),('trip','stop','2026-09-01 05:00-04',3);`)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestPrepareLegacyPreservesEvidenceAndOldWriter(t *testing.T) {
	conn := legacyFixture(t)
	ctx := t.Context()
	for i := 0; i < 2; i++ {
		if err := PrepareLegacy(ctx, conn); err != nil {
			t.Fatal(err)
		}
	}
	var live, archived int
	if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM transit.stop_visit),(SELECT count(*) FROM transit.stop_visit_duplicate_archive)`).Scan(&live, &archived); err != nil {
		t.Fatal(err)
	}
	if live != 2 || archived != 1 {
		t.Fatalf("evidence live=%d archived=%d", live, archived)
	}
	var dates string
	if err := conn.QueryRow(ctx, `SELECT string_agg(service_date::text,',' ORDER BY observed_at) FROM (SELECT * FROM transit.stop_visit UNION ALL SELECT * FROM transit.stop_visit_duplicate_archive) v`).Scan(&dates); err != nil {
		t.Fatal(err)
	}
	if dates != "2026-08-31,2026-08-31,2026-09-01" {
		t.Fatal(dates)
	}
	// Existing writer omits service_date and references its old key throughout prep.
	if _, err := conn.Exec(ctx, `INSERT INTO transit.stop_visit(trip_id,stop_id,observed_at) VALUES('old','stop','2026-09-01 02:00-04') ON CONFLICT(trip_id,stop_id,observed_at) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../../migrations/000028_transit_metric_history.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, strings.Split(string(sql), "-- Reconstructed passages")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO transit.stop_visit(trip_id,stop_id,observed_at) VALUES('old-after-metadata','stop','2026-09-01 02:00-04') ON CONFLICT(trip_id,stop_id,observed_at) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	// HEAD's omitted-column recorder remains compatible after the short key swap.
	if _, err = conn.Exec(ctx, `INSERT INTO transit.stop_visit(trip_id,stop_id,observed_at) VALUES('new','stop','2026-09-01 02:00-04') ON CONFLICT(service_date,trip_id,stop_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var generated string
	if err = conn.QueryRow(ctx, `SELECT attgenerated::text FROM pg_attribute WHERE attrelid='transit.stop_visit'::regclass AND attname='service_date'`).Scan(&generated); err != nil {
		t.Fatal(err)
	}
	if generated != "" {
		t.Fatal("prepared table was rewritten as generated column")
	}
}
