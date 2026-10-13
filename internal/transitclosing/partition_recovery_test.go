package transitclosing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"thundercitizen/internal/transit"
	"thundercitizen/internal/transit/chunk"
	"thundercitizen/internal/transitarchive"
)

func TestClosingProvisionsHistoricalDerivedPartition(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	w := New(db, &memoryStore{})
	w.finalize = func(ctx context.Context, m time.Time) error {
		if err := summaryFinalizer(db)(ctx, m); err != nil {
			return err
		}
		var owner string
		err := db.QueryRow(ctx, `INSERT INTO transit.metric_passage(date,schedule_id,detector_version,trip_id,stop_id,observed_at,source_a,source_b) VALUES('2026-08-31','schedule',1,'old','stop','2026-08-31 12:00-04',1,1) RETURNING tableoid::regclass::text`).Scan(&owner)
		if err != nil {
			return err
		}
		if owner != "transit.metric_passage_p202608" {
			return fmt.Errorf("derived row landed in %s", owner)
		}
		return nil
	}
	if err := w.RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	if journalState(t, db) != "complete" {
		t.Fatal("historical derived month did not close")
	}
	var archived int
	if err := db.QueryRow(ctx, `SELECT (r->>'rows')::int FROM transit.archive_month,jsonb_array_elements(raw_manifest->'tables') r WHERE month='2026-08-01' AND r->>'schema'='transit' AND r->>'name'='metric_passage'`).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("archived passages=%d error=%v", archived, err)
	}
}

func TestClosingRelocatesFrozenFallbackRows(t *testing.T) {
	for _, state := range []string{"frozen", "published"} {
		t.Run(state, func(t *testing.T) {
			db := closingDB(t)
			ctx := context.Background()
			closingExec(t, db, `INSERT INTO transit.metric_passage(date,schedule_id,detector_version,trip_id,stop_id,observed_at,source_a,source_b) VALUES('2026-05-31','schedule',1,'may','stop','2026-05-31 12:00-04',1,1),('2026-06-01','schedule',1,'june','stop','2026-06-01 12:00-04',1,1);
 INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-05-31 12:00-04','may',48.4,-89.2),('2026-06-01 04:00-04','june',48.4,-89.2);`)
			before := map[string]backupFingerprint{}
			for _, table := range []string{"metric_passage", "vehicle_position"} {
				f, err := backupFingerprintTable(ctx, db, "transit."+table, "TRUE")
				if err != nil {
					t.Fatal(err)
				}
				before[table] = f
			}
			_, err := db.Exec(ctx, `INSERT INTO transit.archive_month(month,state,metric_version,raw_prefix,raw_manifest,raw_verified_at,report_base_url,report_manifest) VALUES('2026-05-01',$1,$2,'test','{}',now(),'test','[]')`, state, chunk.Version)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := db.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			month := time.Date(2026, 5, 1, 0, 0, 0, 0, transit.TZ)
			if err := transitarchive.EnsureMonthPartitions(ctx, conn.Conn(), month); err != nil {
				t.Fatal(err)
			}
			for table, want := range before {
				got, err := backupFingerprintTable(ctx, db, "transit."+table, "TRUE")
				if err != nil || got != want {
					t.Fatalf("%s relocation changed rows: %+v -> %+v (%v)", table, want, got, err)
				}
				var moved int
				if err := db.QueryRow(ctx, "SELECT count(*) FROM transit."+table+"_p202605").Scan(&moved); err != nil || moved != 1 {
					t.Fatalf("%s moved=%d error=%v", table, moved, err)
				}
				var guards int
				if err := db.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid IN (to_regclass($1),to_regclass($2)) AND tgname='archive_guard'`, "transit."+table+"_p202605", "transit."+table+"_unassigned").Scan(&guards); err != nil || guards != 2 {
					t.Fatalf("archive guards not restored: %s %d %v", table, guards, err)
				}
			}
			if _, err := db.Exec(ctx, `UPDATE transit.metric_passage SET source_a=2 WHERE date='2026-05-31'`); err == nil {
				t.Fatal("relocated derived rows became writable")
			}
			tag, err := db.Exec(ctx, `UPDATE transit.vehicle_position SET latitude=0 WHERE vehicle_id='may'`)
			if err != nil || tag.RowsAffected() != 0 {
				t.Fatalf("relocated raw guard bypass: %v %v", tag, err)
			}
		})
	}
}
