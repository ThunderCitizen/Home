package transitprepare

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestIncrementalPreparationResumesAfterReleasedTable(t *testing.T) {
	conn := onlineFixture(t)
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-01 12:00+00','original',48.4,-89.2);
 CREATE VIEW transit.block_alert_switch AS SELECT * FROM transit.alert;`)
	var original uint32
	if err := conn.QueryRow(ctx, `SELECT 'transit.vehicle_position'::regclass::oid`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	// GPS should already have released its original storage when alert's unknown
	// dependency blocks progress. Later tables must not have been copied yet.
	if err := PrepareIncremental(ctx, conn); err == nil {
		t.Fatal("dependency did not block alert switch")
	}
	var released, gpsSwapped, alertSwapped, laterCopied bool
	if err := conn.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_class WHERE oid=$1),
 (SELECT swapped FROM transit_prepare.state WHERE table_name='vehicle_position'),
 (SELECT swapped FROM transit_prepare.state WHERE table_name='alert'),
 (SELECT copied FROM transit_prepare.state WHERE table_name='stop_delay')`, original).Scan(&released, &gpsSwapped, &alertSwapped, &laterCopied); err != nil {
		t.Fatal(err)
	}
	if !released || !gpsSwapped || alertSwapped || laterCopied {
		t.Fatalf("released=%v gps=%v alert=%v later=%v", released, gpsSwapped, alertSwapped, laterCopied)
	}
	var paused bool
	if err := conn.QueryRow(ctx, `SELECT bool_and(coalesce('autovacuum_enabled=false'=ANY(c.reloptions),false)) FROM pg_partition_tree('transit.vehicle_position') p JOIN pg_class c ON c.oid=p.relid WHERE p.isleaf`).Scan(&paused); err != nil || !paused {
		t.Fatalf("early switch allowed maintenance: %v %v", paused, err)
	}
	// A resumed deployment also reconciles tables switched by an older preparer.
	exec(`ALTER TABLE transit.vehicle_position_p202608 RESET (autovacuum_enabled)`)
	// Legacy inserts still work during the mixed-layout interval; identity must
	// advance and the next preparation must neither recopy nor lose these writes.
	exec(`INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-09-01 12:00+00','after-switch',48.4,-89.2);
 INSERT INTO transit.alert(feed_timestamp,alert_id) VALUES('2026-08-01 12:00+00','pending') ON CONFLICT(alert_id,feed_timestamp) DO NOTHING;
 DROP VIEW transit.block_alert_switch;`)
	if err := PrepareIncremental(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err := PrepareIncremental(ctx, conn); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO transit.alert(feed_timestamp,alert_id) VALUES('2026-08-01 12:00+00','pending') ON CONFLICT(alert_id,feed_timestamp) DO NOTHING`)
	sql, err := os.ReadFile("../../migrations/000031_transit_month_close.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = Cutover(ctx, conn, string(sql)); err != nil {
		t.Fatal(err)
	}
	var gps, ids, alerts, version int
	if err = conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM transit.vehicle_position),(SELECT count(DISTINCT id) FROM transit.vehicle_position),(SELECT count(*) FROM transit.alert),(SELECT version FROM schema_migrations)`).Scan(&gps, &ids, &alerts, &version); err != nil {
		t.Fatal(err)
	}
	var disabled int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='transit' AND coalesce('autovacuum_enabled=false'=ANY(c.reloptions),false)`).Scan(&disabled); err != nil || disabled != 0 {
		t.Fatalf("maintenance not restored: %d %v", disabled, err)
	}
	if gps != 2 || ids != 2 || alerts != 1 || version != 31 {
		t.Fatalf("gps=%d ids=%d alerts=%d version=%d", gps, ids, alerts, version)
	}
	if err = PrepareIncremental(ctx, conn); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalHandoffWaitsForWriterAndPreservesDelta(t *testing.T) {
	conn := onlineFixture(t)
	ctx := t.Context()
	if err := Prepare(ctx, conn); err != nil {
		t.Fatal(err)
	}
	writer, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close(context.Background())
	tx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-01 12:00+00','late-commit',48.4,-89.2)`); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err = switchCompatibleTable(short, conn, "vehicle_position"); err == nil {
		t.Fatal("switched with uncommitted writer")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = switchCompatibleTable(ctx, conn, "vehicle_position"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM transit.vehicle_position WHERE vehicle_id='late-commit'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("late writer: %d %v", count, err)
	}
	if err = switchCompatibleTable(ctx, conn, "cancellation"); err == nil {
		t.Fatal("unsafe early writer transition accepted")
	}
}

func TestPreparationReclaimsUnfinishedCopyOnRetry(t *testing.T) {
	conn := onlineFixture(t)
	ctx := t.Context()
	if _, err := conn.Exec(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-01 12:00+00','original',48.4,-89.2)`); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(ctx, conn); err != nil {
		t.Fatal(err)
	}
	// Model an interrupted copy: its checkpoint never committed, but the shadow
	// relation has allocated storage. Retry must replace its files, not append.
	if _, err := conn.Exec(ctx, `UPDATE transit_prepare.state SET copied=false,snapshot=NULL WHERE table_name='vehicle_position'`); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO transit_prepare.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) SELECT '2026-08-01 12:00+00','aborted',48.4,-89.2 FROM generate_series(1,10000)`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var before, after uint32
	query := `SELECT pg_relation_filenode('transit_prepare.vehicle_position_p202608')`
	if err = conn.QueryRow(ctx, query).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = Prepare(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, query).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("retry retained unfinished copy's files")
	}
	var count int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM transit_prepare.vehicle_position`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retry rows=%d err=%v", count, err)
	}
}

func TestIncrementalSwitchDrainsOnlyItsBoundedTail(t *testing.T) {
	conn := onlineFixture(t)
	ctx := t.Context()
	if err := Prepare(ctx, conn); err != nil {
		t.Fatal(err)
	}
	// Another table's earlier event must not consume the final replay budget.
	if _, err := conn.Exec(ctx, `INSERT INTO transit.alert(feed_timestamp,alert_id) VALUES('2026-08-01 12:00+00','other-table'); INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-01 12:00+00','tail',48.4,-89.2)`); err != nil {
		t.Fatal(err)
	}
	if err := switchTable(ctx, conn, "vehicle_position"); err != nil {
		t.Fatal(err)
	}
	var gps, other, pending int
	if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM transit.vehicle_position WHERE vehicle_id='tail'),(SELECT count(*) FROM transit_prepare.delta WHERE table_name='alert'),(SELECT count(*) FROM transit_prepare.delta WHERE table_name='vehicle_position')`).Scan(&gps, &other, &pending); err != nil {
		t.Fatal(err)
	}
	if gps != 1 || other != 1 || pending != 0 {
		t.Fatalf("gps=%d other=%d pending=%d", gps, other, pending)
	}
	if _, err := Replay(ctx, conn, 100); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalSwitchWithContinuousReadersAndWriters(t *testing.T) {
	conn := onlineFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var seed int64
	if err := conn.QueryRow(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-01 12:00+00','seed',48.4,-89.2) RETURNING id`).Scan(&seed); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(ctx, conn); err != nil {
		t.Fatal(err)
	}
	writer, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		ids []int64
		err error
	}
	written := make(chan result, 1)
	read := make(chan error, 1)
	ready := make(chan struct{})
	switched := make(chan struct{})
	go func() {
		defer writer.Close(context.Background())
		r := result{}
		defer func() { written <- r }()
		for i := 0; ; i++ {
			if i >= 200 {
				select {
				case <-switched:
					return
				default:
				}
			}
			var id int64
			r.err = writer.QueryRow(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-01 12:00+00','continuous',48.4,-89.2) RETURNING id`).Scan(&id)
			if r.err != nil {
				return
			}
			r.ids = append(r.ids, id)
			if i == 10 {
				close(ready)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}()
	go func() {
		defer reader.Close(context.Background())
		for i := 0; ; i++ {
			if i >= 300 {
				select {
				case <-switched:
					read <- nil
					return
				default:
				}
			}
			var found bool
			err := reader.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM transit.vehicle_position WHERE id=$1)`, seed).Scan(&found)
			if err != nil {
				read <- err
				return
			}
			if !found {
				read <- fmt.Errorf("baseline row disappeared")
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = switchCompatibleTable(ctx, conn, "vehicle_position"); err != nil {
		t.Fatal(err)
	}
	close(switched)
	w := <-written
	if w.err != nil {
		t.Fatal(w.err)
	}
	if err = <-read; err != nil {
		t.Fatal(err)
	}
	var count int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM transit.vehicle_position WHERE id=ANY($1)`, w.ids).Scan(&count); err != nil || count != len(w.ids) || count < 200 {
		t.Fatalf("surviving writes=%d err=%v", count, err)
	}
}
