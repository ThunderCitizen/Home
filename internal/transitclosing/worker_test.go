package transitclosing

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/transit"
	"thundercitizen/internal/transit/chunk"
	"thundercitizen/internal/transitarchive"
)

func reportEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	if len(out) != 2 || out[transit.MonthlyReportFilename()] == nil || out[transit.DailyReportFilename()] == nil {
		t.Fatalf("unexpected bundle entries: %v", out)
	}
	return out
}

type memoryStore struct {
	files  map[string][]byte
	public map[string]bool
	calls  int
	fail   bool
	hook   func(string) error
}

func (s *memoryStore) PublicURL(prefix string) string {
	return "https://reports.example.test/" + prefix
}
func (s *memoryStore) UploadVerified(_ context.Context, key, file string, public bool) (Object, error) {
	s.calls++
	if s.fail {
		return Object{}, fmt.Errorf("injected upload failure")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return Object{}, err
	}
	if s.files == nil {
		s.files = map[string][]byte{}
		s.public = map[string]bool{}
	}
	s.files[key] = append([]byte(nil), data...)
	s.public[key] = public
	if s.hook != nil {
		if err = s.hook(key); err != nil {
			return Object{}, err
		}
	}
	sum := sha256.Sum256(data)
	return Object{Key: key, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))}, nil
}

func closingDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TRANSIT_ARCHIVE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TRANSIT_ARCHIVE_TEST_DATABASE_URL; only disposable databases are modified")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("transit_closing_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close(ctx)
	})
	files, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file) > "000031_transit_month_close.up.sql" {
			break
		}
		if strings.HasPrefix(filepath.Base(file), "000030") {
			closingExec(t, db, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-31 12:00-04','old',48.4,-89.2),('2026-09-01 04:00-04','current',48.4,-89.2);
    INSERT INTO transit.cancellation(feed_timestamp,trip_id,route_id,schedule_relationship,start_date) VALUES('2026-08-31 12:00-04','old','route','CANCELED','20260831'),('2026-08-31 12:00-04','future','route','CANCELED','20260901');
 INSERT INTO transit.metric_schedule(id,source,published_at) VALUES('schedule','test','2026-08-01');
 INSERT INTO transit.metric_service(schedule_id,service_id,date) VALUES('schedule','weekday','2026-08-31');
 INSERT INTO transit.metric_trip(schedule_id,trip_id,route_id,service_id,direction_id,first_departure,stops) VALUES('schedule','old','route','weekday',0,21600,'[]');`)
		}
		sql, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	closingExec(t, db, `CREATE TABLE schema_migrations(version bigint,dirty boolean);INSERT INTO schema_migrations VALUES(31,false)`)
	return db
}
func closingExec(t *testing.T, db *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}
func journalState(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	var state string
	if err := db.QueryRow(context.Background(), `SELECT state FROM transit.archive_month WHERE month='2026-08-01'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}
func summaryFinalizer(db *pgxpool.Pool) func(context.Context, time.Time) error {
	return func(ctx context.Context, m time.Time) error {
		_, err := db.Exec(ctx, `INSERT INTO transit.route_day_summary(route_id,date,metric_version,trip_count,scheduled_count,cancelled_count,no_notice_count,cancellation_scheduled_count,otp_count,otp_on_time,early_count,late_count,expected_timepoints,observed_timepoints,eligible_windows,total_windows,wait_observed_area,wait_scheduled_area,window_seconds,cv_weighted_sum,cv_weight,schedule_id,built_at,is_final) VALUES('route',$1::date,$2,9,10,1,0,10,9,8,0,1,9,9,0,0,0,0,0,0,0,'schedule',now(),true)`, m, chunk.Version)
		if err != nil {
			return err
		}
		_, err = db.Exec(ctx, `INSERT INTO transit.route_band_chunk(route_id,date,band,service_kind,trip_count,on_time_count,scheduled_count,cancelled_count,no_notice_count,headway_count,headway_sum_sec,headway_sum_sec_sq,sched_headway_sec,metric_version) VALUES('route',$1::date,'morning','weekday',9,8,10,1,0,0,0,0,0,$2)`, m, chunk.Version)
		return err
	}
}
func septemberBoundary() time.Time { return time.Date(2026, 9, 1, 4, 0, 0, 0, transit.TZ) }

func TestClosingBoundaryAndPublication(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	store := &memoryStore{}
	w := New(db, store)
	w.finalize = summaryFinalizer(db)
	if err := w.RunOnce(ctx, septemberBoundary().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM transit.archive_month`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("closed before boundary: %d %v", count, err)
	}
	if err := w.RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	if state := journalState(t, db); state != "complete" {
		t.Fatal(state)
	}
	for table, want := range map[string]int{"vehicle_position": 1, "cancellation": 1, "route_day_summary": 1, "route_band_chunk": 1} {
		if err := db.QueryRow(ctx, "SELECT count(*) FROM transit."+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count %d want %d: %v", table, count, want, err)
		}
	}
	var base string
	if err := db.QueryRow(ctx, `SELECT report_base_url FROM transit.archive_month`).Scan(&base); err != nil || !strings.HasPrefix(base, "https://reports.example.test/transit/reports/archive/2026-08/") {
		t.Fatalf("report pointer %s %v", base, err)
	}
	public := 0
	for key, data := range store.files {
		if store.public[key] {
			public++
			if !strings.HasSuffix(key, ".zip") || !strings.Contains(string(reportEntries(t, data)[transit.MonthlyReportFilename()]), "2026-08") {
				t.Fatalf("public report %s", key)
			}
		}
	}
	if public != 1 {
		t.Fatalf("public files %d", public)
	}
	result, err := db.Exec(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-31 12:00-04','stale',48.4,-89.2),('2026-09-01 05:00-04','fresh',48.4,-89.2)`)
	if err != nil || result.RowsAffected() != 1 {
		t.Fatalf("post-close mixed feed batch: %v %v", result, err)
	}
	calls := store.calls
	if err = w.RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	if store.calls != calls {
		t.Fatal("completed month uploaded twice")
	}
}

func TestClosingUploadAndEvictionRetries(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	store := &memoryStore{fail: true}
	w := New(db, store)
	finalized := 0
	finalize := summaryFinalizer(db)
	w.finalize = func(ctx context.Context, m time.Time) error { finalized++; return finalize(ctx, m) }
	if err := w.RunOnce(ctx, septemberBoundary()); err == nil {
		t.Fatal("injected failure ignored")
	}
	if state := journalState(t, db); state != "frozen" {
		t.Fatal(state)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM transit.vehicle_position`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("raw removed on failed upload: %d %v", count, err)
	}
	store.fail = false
	store.hook = func(key string) error {
		if strings.HasSuffix(key, transit.ReportBundleFilename()) {
			_, err := db.Exec(ctx, `CREATE VIEW transit.prevent_eviction AS SELECT * FROM transit.vehicle_position_p202608`)
			return err
		}
		return nil
	}
	if err := w.RunOnce(ctx, septemberBoundary()); err == nil {
		t.Fatal("dependent-view eviction failure ignored")
	}
	if state := journalState(t, db); state != "published" {
		t.Fatal(state)
	}
	if finalized != 1 {
		t.Fatalf("finalization repeated %d times", finalized)
	}
	calls := store.calls
	store.hook = nil
	// Simulate restart immediately after upload's durable journal commit.
	closingExec(t, db, `UPDATE transit.archive_month SET state='uploaded' WHERE month='2026-08-01'`)
	if err := w.RunOnce(ctx, septemberBoundary()); err == nil {
		t.Fatal("view no longer blocks eviction")
	}
	if store.calls != calls {
		t.Fatal("uploaded release uploaded twice")
	}
	closingExec(t, db, `DROP VIEW transit.prevent_eviction`)
	if err := w.RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	if journalState(t, db) != "complete" || store.calls != calls {
		t.Fatal("published retry did not finish without upload")
	}
}

func TestClosingDisabled(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	w := New(db, nil)
	w.finalize = func(context.Context, time.Time) error { t.Fatal("disabled worker finalized"); return nil }
	if err := w.RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM transit.archive_month`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("disabled journal %d %v", count, err)
	}
}

func TestClosingDrainsActiveRecorder(t *testing.T) {
	db := closingDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-31 13:00-04','inflight',48.4,-89.2)`); err != nil {
		t.Fatal(err)
	}
	w := New(db, &memoryStore{})
	finalize := summaryFinalizer(db)
	w.finalize = func(ctx context.Context, m time.Time) error {
		var count int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM transit.vehicle_position WHERE vehicle_id='inflight'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("finalization did not include drained writer")
		}
		return finalize(ctx, m)
	}
	done := make(chan error, 1)
	go func() { done <- w.RunOnce(ctx, septemberBoundary()) }()
	// Observe the actual blocked advisory lock instead of relying on a sleep to
	// infer that the writer and closer overlapped.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND classid=23010 AND objid=$1 AND NOT granted)`, 2026*12+8).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("closer returned before writer drained: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	var rows int
	if err = db.QueryRow(ctx, `SELECT (t->>'rows')::int FROM transit.archive_month a,jsonb_array_elements(a.raw_manifest->'tables') t WHERE a.month='2026-08-01' AND t->>'schema'='transit' AND t->>'name'='vehicle_position'`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("drained archive count %d %v", rows, err)
	}
}

func TestClosingMissingScheduleDoesNotSeal(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	store := &memoryStore{}
	w := New(db, store)
	w.finalize = summaryFinalizer(db)
	closingExec(t, db, `DELETE FROM transit.metric_service`)
	if err := w.RunOnce(ctx, septemberBoundary()); err == nil {
		t.Fatal("missing archived schedule did not block close")
	}
	if state := journalState(t, db); state != "preparing" {
		t.Fatalf("premature seal: %s", state)
	}
	if store.calls != 0 {
		t.Fatal("uploaded without archived schedule")
	}
	result, err := db.Exec(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-08-31 14:00-04','still-open',48.4,-89.2)`)
	if err != nil || result.RowsAffected() != 1 {
		t.Fatalf("preparing month stopped ingesting: %v %v", result, err)
	}
	closingExec(t, db, `INSERT INTO transit.metric_service(schedule_id,service_id,date) VALUES('schedule','weekday','2026-08-31')`)
	if err = w.RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	if journalState(t, db) != "complete" {
		t.Fatal("coverage repair failed to resume close")
	}
}

func TestClosingRealFinalizerAndRestore(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	store := &memoryStore{}
	if err := New(db, store).RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	var cancelled, scheduled int
	var final bool
	if err := db.QueryRow(ctx, `SELECT cancelled_count,scheduled_count,is_final FROM transit.route_day_summary WHERE route_id='route' AND date='2026-08-31'`).Scan(&cancelled, &scheduled, &final); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || scheduled != 1 || !final {
		t.Fatalf("final summary %d/%d final=%v", cancelled, scheduled, final)
	}
	// Restore exactly the uploaded private evidence into a separate analysis schema.
	dir := t.TempDir()
	for key, data := range store.files {
		if !store.public[key] {
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(key)), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if err := transitarchive.Restore(ctx, conn.Conn(), dir, "transit_archive_roundtrip"); err != nil {
		t.Fatal(err)
	}
	var restored int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM transit_archive_roundtrip.transit__vehicle_position`).Scan(&restored); err != nil || restored != 1 {
		t.Fatalf("restored raw count=%d err=%v", restored, err)
	}
}

func TestClosingEmptyMonthDoesNotBlockCatchup(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	closingExec(t, db, `DELETE FROM transit.vehicle_position; DELETE FROM transit.cancellation; DELETE FROM transit.metric_service; DELETE FROM transit.metric_trip`)
	store := &memoryStore{}
	if err := New(db, store).RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	if state := journalState(t, db); state != "complete" {
		t.Fatal(state)
	}
	for key, data := range store.files {
		if store.public[key] {
			for _, csv := range reportEntries(t, data) {
				if strings.Count(string(csv), "\n") != 1 {
					t.Fatalf("empty month invented statistics: %s", csv)
				}
			}
		}
	}
	// With no new service, September must also close on the next month boundary.
	if err := New(db, store).RunOnce(ctx, septemberBoundary().AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	var done int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM transit.archive_month WHERE state='complete'`).Scan(&done); err != nil || done != 2 {
		t.Fatalf("empty month blocked catchup: %d %v", done, err)
	}
}
