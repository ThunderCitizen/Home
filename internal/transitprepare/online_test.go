package transitprepare

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func onlineFixture(t *testing.T) *pgx.Conn { return migrationFixture(t, 29) }

func migrationFixture(t *testing.T, version int) *pgx.Conn {
	t.Helper()
	conn := legacyFixture(t)
	ctx := t.Context()
	if _, err := conn.Exec(ctx, fmt.Sprintf("DROP SCHEMA transit CASCADE;UPDATE schema_migrations SET version=%d", version)); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file)[:6] > fmt.Sprintf("%06d", version) {
			break
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Exec(ctx, string(b)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	return conn
}
func TestOnlineCopyDeltaReplayAndAtomicCutover(t *testing.T) {
	conn := onlineFixture(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-01 12:00+00','original',48.4,-89.2);
 INSERT INTO transit.stop_visit(trip_id,stop_id,route_id,vehicle_id,observed_at,entered_at,distance_m) VALUES('trip','stop','route','bus','2026-08-01 12:00+00','2026-08-01 12:00+00',1);
 INSERT INTO transit.stop_visit_duplicate_archive SELECT * FROM transit.stop_visit; INSERT INTO transit.stop_visit_duplicate_archive SELECT * FROM transit.stop_visit;`)
	sql, err := os.ReadFile("../../migrations/000031_transit_month_close.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = BootstrapMetadata(ctx, conn, string(sql)); err != nil {
		t.Fatal(err)
	}
	if err = Prepare(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err = Prepare(ctx, conn); err != nil {
		t.Fatal(err)
	} // Resume without recopy.
	// One uncommitted lower sequence must not be skipped by replaying later commits.
	writer, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close(context.Background())
	tx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-09-01 12:00+00','pending',48.4,-89.2)`); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE transit.vehicle_position SET feed_timestamp='2026-09-02 12:00+00',longitude=-89.3 WHERE vehicle_id='original';
 INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-03 12:00+00','deleted',48.4,-89.2);
 DELETE FROM transit.vehicle_position WHERE vehicle_id='deleted';
 DELETE FROM transit.stop_visit_duplicate_archive WHERE ctid IN (SELECT ctid FROM transit.stop_visit_duplicate_archive LIMIT 1);`)
	if _, err = Replay(ctx, conn, 100); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Snapshot-visible changes must not be reapplied. Recopy a tiny table with
	// its trigger already installed, exactly like resuming an interrupted copy.
	exec(`INSERT INTO transit.stop_visit_duplicate_archive SELECT * FROM transit.stop_visit; TRUNCATE transit_prepare.stop_visit_duplicate_archive`)
	if err = copyTable(ctx, conn, "stop_visit_duplicate_archive"); err != nil {
		t.Fatal(err)
	}
	if _, err = Replay(ctx, conn, 100); err != nil {
		t.Fatal(err)
	}
	// Unknown dependencies must make cutover fully roll back.
	exec(`CREATE VIEW transit.prepare_dependency AS SELECT * FROM transit.vehicle_position`)
	if err = Cutover(ctx, conn, string(sql)); err == nil {
		t.Fatal("cutover accepted dependent view")
	}
	exec(`DROP VIEW transit.prepare_dependency`)
	// Row multiset comparison, including duplicate rows and cross-month updates.
	for _, name := range []string{"vehicle_position", "stop_visit", "stop_visit_duplicate_archive"} {
		var same bool
		err = conn.QueryRow(ctx, "SELECT NOT EXISTS((SELECT * FROM "+ident("transit", name)+" EXCEPT ALL SELECT * FROM "+ident("transit_prepare", name)+") UNION ALL (SELECT * FROM "+ident("transit_prepare", name)+" EXCEPT ALL SELECT * FROM "+ident("transit", name)+"))").Scan(&same)
		if err != nil || !same {
			t.Fatalf("%s mismatch: %v", name, err)
		}
	}
	if err = Cutover(ctx, conn, string(sql)); err != nil {
		t.Fatal(err)
	}
	if err = Cutover(ctx, conn, string(sql)); err != nil {
		t.Fatal(err)
	}
	var version int
	var dirty bool
	if err = conn.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil || version != 31 || dirty {
		t.Fatalf("version %d dirty %v: %v", version, dirty, err)
	}
	// Current writer conflict keys and identity allocation work after cutover.
	exec(`INSERT INTO transit.cancellation(feed_timestamp,trip_id,route_id,schedule_relationship,start_date) VALUES('2026-09-01 12:00+00','trip','route','CANCELED','20260901') ON CONFLICT(trip_id,feed_timestamp,start_date) DO NOTHING;
 INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-09-01 12:00+00','new',48.4,-89.2);`)
	var parents int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM pg_partitioned_table p JOIN pg_class c ON c.oid=p.partrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='transit'`).Scan(&parents); err != nil || parents != 8 {
		t.Fatalf("parents %d: %v", parents, err)
	}
	// The default entry path remains valid for offline/dev migrations too.
	if strings.Contains(string(sql), "transit_prepare") {
		t.Fatal("migration unexpectedly coupled to staging schema")
	}
}

func TestPreparationMetadataDoesNotQueueReaders(t *testing.T) {
	conn := migrationFixture(t, 22)
	ctx := t.Context()
	if err := PrepareLegacy(ctx, conn); err != nil {
		t.Fatal(err)
	}
	blocker, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close(context.Background())
	tx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `LOCK TABLE transit.vehicle_position IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ApplyMetadata(waitCtx, conn, "../../migrations") }()
	time.Sleep(100 * time.Millisecond)
	reader, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(context.Background())
	readCtx, readCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer readCancel()
	var count int
	if err = reader.QueryRow(readCtx, `SELECT count(*) FROM transit.vehicle_position`).Scan(&count); err != nil {
		t.Fatalf("metadata lock queued a reader: %v", err)
	}
	cancel()
	if err = <-done; err == nil {
		t.Fatal("metadata ran despite held lock")
	}
	var version int
	var dirty bool
	if err = reader.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil || version != 22 || dirty {
		t.Fatalf("failed retry changed schema marker: %d/%v %v", version, dirty, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = ApplyMetadata(ctx, conn, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if err = ApplyMetadata(ctx, conn, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil || version != 29 || dirty {
		t.Fatalf("bad prepared version %d/%v %v", version, dirty, err)
	}
}
