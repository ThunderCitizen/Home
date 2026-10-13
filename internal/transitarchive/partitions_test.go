package transitarchive

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPartitionOperatingBounds(t *testing.T) {
	m, _ := time.Parse("2006-01", "2026-11")
	lo, hi := partitionBounds(PartitionTable{Timestamp: true}, m)
	if lo != "2026-11-01T04:00:00-05:00" || hi != "2026-12-01T04:00:00-05:00" {
		t.Fatalf("DST bounds: %s %s", lo, hi)
	}
	m, _ = time.Parse("2006-01", "2026-03")
	lo, hi = partitionBounds(PartitionTable{Timestamp: true}, m)
	if lo != "2026-03-01T04:00:00-05:00" || hi != "2026-04-01T04:00:00-04:00" {
		t.Fatalf("DST bounds: %s %s", lo, hi)
	}
}

func TestPartitionMigrationAndEviction(t *testing.T) {
	url := os.Getenv("TRANSIT_ARCHIVE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TRANSIT_ARCHIVE_TEST_DATABASE_URL; only a disposable database is modified")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("transit_archive_partition_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	cfg, _ := pgx.ParseConfig(url)
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	exec := func(sql string) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE SCHEMA transit;
 CREATE TABLE transit.vehicle_position(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,feed_timestamp timestamptz NOT NULL);
 CREATE INDEX gps_time ON transit.vehicle_position(feed_timestamp);
 CREATE TABLE transit.alert(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,feed_timestamp timestamptz NOT NULL,alert_id text,UNIQUE(alert_id,feed_timestamp));
 CREATE TABLE transit.cancellation(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,feed_timestamp timestamptz NOT NULL,trip_id text,start_date text,UNIQUE(trip_id,feed_timestamp));
 CREATE TABLE transit.stop_delay(date date PRIMARY KEY);
 CREATE TABLE transit.vehicle_assignment(date date PRIMARY KEY);
 CREATE TABLE transit.metric_passage(date date PRIMARY KEY);
 CREATE TABLE transit.stop_visit(observed_at timestamptz NOT NULL,service_date date GENERATED ALWAYS AS (((observed_at AT TIME ZONE 'America/Thunder_Bay')-interval '4 hours')::date) STORED,trip_id text,stop_id text,PRIMARY KEY(service_date,trip_id,stop_id));
 CREATE TABLE transit.stop_visit_duplicate_archive AS SELECT * FROM transit.stop_visit WITH NO DATA;
 INSERT INTO transit.vehicle_position(feed_timestamp) VALUES('2026-09-01 03:59-04'),('2026-09-01 04:00-04');
 INSERT INTO transit.cancellation(feed_timestamp,trip_id,start_date) VALUES('2026-08-31 12:00-04','future','20260901'),('2026-08-31 12:00-04','unknown',NULL);
 INSERT INTO transit.stop_visit(observed_at,trip_id,stop_id) VALUES('2026-08-31 12:00-04','t','s');
 INSERT INTO transit.stop_delay VALUES('2026-08-31');INSERT INTO transit.vehicle_assignment VALUES('2026-08-31');INSERT INTO transit.metric_passage VALUES('2026-08-31');`)
	migration, err := os.ReadFile("../../migrations/000030_transit_raw_partitions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	var count int
	var table string
	if err = conn.QueryRow(ctx, `SELECT tableoid::regclass::text FROM transit.vehicle_position WHERE id=1`).Scan(&table); err != nil || table != "transit.vehicle_position_p202608" {
		t.Fatalf("boundary %s %v", table, err)
	}
	exec(`INSERT INTO transit.vehicle_position(feed_timestamp) VALUES('2026-09-02 12:00-04'); INSERT INTO transit.cancellation(feed_timestamp,trip_id,start_date) VALUES('2026-08-31 12:00-04','unknown',NULL) ON CONFLICT(trip_id,feed_timestamp,start_date) DO NOTHING;`)
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM transit.cancellation`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("dedup %d %v", count, err)
	}
	exec(`CREATE TABLE transit.archive_month(month date PRIMARY KEY,state text,raw_manifest jsonb,raw_verified_at timestamptz);INSERT INTO transit.archive_month VALUES('2026-08-01','sealed',NULL,NULL)`)
	tx, _ := conn.Begin(ctx)
	if err = DropMonthPartitions(ctx, tx, "2026-08"); err == nil {
		t.Fatal("unverified eviction allowed")
	}
	tx.Rollback(ctx)
	exec(`UPDATE transit.archive_month SET state='published',raw_manifest='{}',raw_verified_at=now()`)
	tx, _ = conn.Begin(ctx)
	if err = DropMonthPartitions(ctx, tx, "2026-08"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM transit.vehicle_position`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("GPS retention %d %v", count, err)
	}
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM transit.cancellation`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("future/default retention %d %v", count, err)
	}
	if err = EnsurePartitions(ctx, conn, time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO transit.vehicle_position(feed_timestamp) VALUES('2030-01-02 12:00-05')`)
	if err = EnsurePartitions(ctx, conn, time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT tableoid::regclass::text FROM transit.vehicle_position WHERE feed_timestamp='2030-01-02 12:00-05'`).Scan(&table); err != nil || table != "transit.vehicle_position_p203001" {
		t.Fatalf("default relocation: %s %v", table, err)
	}
	exec(`INSERT INTO transit.archive_month VALUES('2026-07-01','published','{}',now()); INSERT INTO transit.vehicle_position(feed_timestamp) VALUES('2026-07-15 12:00-04')`)
	tx, _ = conn.Begin(ctx)
	if err = DropMonthPartitions(ctx, tx, "2026-07"); err == nil {
		t.Fatal("eviction allowed with unassigned raw rows")
	}
	tx.Rollback(ctx)
}

// Exercise the production DDL, including PostGIS triggers, identity ownership,
// indexes and metric schedule foreign keys, in an isolated database.
func TestPartitionProductionSchema(t *testing.T) {
	url := os.Getenv("TRANSIT_ARCHIVE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TRANSIT_ARCHIVE_TEST_DATABASE_URL for disposable production-schema migration")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("transit_archive_schema_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	cfg, _ := pgx.ParseConfig(url)
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	exec := func(sql string) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file) > "000030_transit_raw_partitions.up.sql" {
			break
		}
		if strings.HasPrefix(filepath.Base(file), "000030") {
			exec(`INSERT INTO transit.vehicle_position(id,feed_timestamp,vehicle_id,latitude,longitude) OVERRIDING SYSTEM VALUE VALUES(101,'2026-08-31 12:00-04','bus',48.4,-89.2);
    INSERT INTO transit.alert(id,feed_timestamp,alert_id) OVERRIDING SYSTEM VALUE VALUES(201,'2026-08-31 12:00-04','alert');
    INSERT INTO transit.cancellation(id,feed_timestamp,trip_id,route_id,schedule_relationship,start_date) OVERRIDING SYSTEM VALUE VALUES(301,'2026-08-31 12:00-04','trip','route','CANCELED','20260831');
    INSERT INTO transit.metric_schedule(id,source,published_at) VALUES('schedule','test','2026-08-01');
    INSERT INTO transit.metric_passage VALUES('2026-08-31','schedule',1,'trip','stop','2026-08-31 12:00-04',101,101);
    INSERT INTO transit.stop_visit(trip_id,stop_id,route_id,vehicle_id,observed_at,distance_m,entered_at) VALUES('trip','stop','route','bus','2026-08-31 12:00-04',0,'2026-08-31 12:00-04');`)
		}
		migration, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("migration %s: %v", file, err)
		}
	}
	var id int64
	var geog bool
	err = conn.QueryRow(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-31 13:00-04','bus',48.4,-89.2) RETURNING id,geog IS NOT NULL`).Scan(&id, &geog)
	if err != nil || id != 102 || !geog {
		t.Fatalf("identity/geography trigger: id=%d geog=%v err=%v", id, geog, err)
	}
	err = conn.QueryRow(ctx, `INSERT INTO transit.alert(feed_timestamp,alert_id) VALUES('2026-08-31 13:00-04','alert') RETURNING id`).Scan(&id)
	if err != nil || id != 202 {
		t.Fatalf("alert identity %d %v", id, err)
	}
	err = conn.QueryRow(ctx, `INSERT INTO transit.cancellation(feed_timestamp,trip_id,route_id,schedule_relationship,start_date) VALUES('2026-08-31 13:00-04','trip','route','CANCELED','20260831') RETURNING id`).Scan(&id)
	if err != nil || id != 302 {
		t.Fatalf("cancellation identity %d %v", id, err)
	}
	if _, err = conn.Exec(ctx, `DELETE FROM transit.metric_schedule WHERE id='schedule'`); err == nil {
		t.Fatal("metric schedule foreign key lost")
	}
	var count int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM pg_index WHERE indrelid='transit.vehicle_position'::regclass`).Scan(&count); err != nil || count < 2 {
		t.Fatalf("GPS indexes lost %d %v", count, err)
	}
	exec(`INSERT INTO transit.stop_visit(trip_id,stop_id,route_id,vehicle_id,observed_at,distance_m,service_date,entered_at) VALUES('trip','stop','route','bus','2026-08-31 13:00-04',0,'2026-08-31','2026-08-31 13:00-04') ON CONFLICT(service_date,trip_id,stop_id) DO NOTHING`)
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM transit.stop_visit`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("stop visit dedup lost %d %v", count, err)
	}
	journal, err := os.ReadFile("../../migrations/000031_transit_month_close.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(journal))
	exec(`INSERT INTO transit.archive_month(month,state,metric_version) VALUES('2026-08-01','sealed',1)`)
	result, err := conn.Exec(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-31 14:00-04','stale',48.4,-89.2)`)
	if err != nil || result.RowsAffected() != 0 {
		t.Fatalf("sealed raw insert not skipped: %v %v", result, err)
	}
	exec(`UPDATE transit.archive_month SET state='frozen'`)
	if _, err = conn.Exec(ctx, `DELETE FROM transit.metric_passage WHERE date='2026-08-31'`); err == nil {
		t.Fatal("frozen derived delete allowed")
	}
	exec(`UPDATE transit.archive_month SET state='published',raw_prefix='archive',raw_manifest='{}',raw_verified_at=now(),report_base_url='https://example.test',report_manifest='{}'`)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = DropMonthPartitions(ctx, tx, "2026-08"); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result, err = conn.Exec(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-31 14:00-04','stale',48.4,-89.2),('2026-09-01 04:00-04','current',48.4,-89.2)`)
	if err != nil || result.RowsAffected() != 1 {
		t.Fatalf("fallback stale/current mixed insert: %v %v", result, err)
	}
}
