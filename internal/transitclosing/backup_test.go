package transitclosing

// This opt-in rehearsal consumes an operator-restored backup in an isolated DB.
// It never creates/modifies the app database or writes to the source backup.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/transit"
	"thundercitizen/internal/transitarchive"
)

type backupFingerprint struct {
	Rows int64  `json:"rows"`
	Hash string `json:"multiset_hash_sum"`
}

func backupFingerprintTable(ctx context.Context, db *pgxpool.Pool, table, predicate string) (backupFingerprint, error) {
	var f backupFingerprint
	err := db.QueryRow(ctx, "SELECT count(*),coalesce(sum(hashtextextended(to_jsonb(t)::text,0)::numeric),0)::text FROM "+table+" t WHERE "+predicate).Scan(&f.Rows, &f.Hash)
	return f, err
}

type diskRehearsalStore struct {
	root       string
	calls      int
	failReport bool
}

func (s *diskRehearsalStore) PublicURL(prefix string) string {
	return "https://rehearsal.invalid/" + prefix
}
func (s *diskRehearsalStore) UploadVerified(ctx context.Context, key, file string, public bool) (Object, error) {
	s.calls++
	if public && strings.HasSuffix(key, transit.ReportBundleFilename()) && s.failReport {
		return Object{}, fmt.Errorf("injected failure after raw archive and monthly CSV upload")
	}
	dest := filepath.Join(s.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return Object{}, err
	}
	src, err := os.Open(file)
	if err != nil {
		return Object{}, err
	}
	defer src.Close()
	out, err := os.Create(dest)
	if err != nil {
		return Object{}, err
	}
	expected := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, expected), src)
	closeErr := out.Close()
	if err != nil {
		return Object{}, err
	}
	if closeErr != nil {
		return Object{}, closeErr
	}
	got, err := os.Open(dest)
	if err != nil {
		return Object{}, err
	}
	defer got.Close()
	actual := sha256.New()
	read, err := io.Copy(actual, got)
	if err != nil {
		return Object{}, err
	}
	if read != size || !reflect.DeepEqual(expected.Sum(nil), actual.Sum(nil)) {
		return Object{}, fmt.Errorf("read-back mismatch for %s", key)
	}
	return Object{Key: key, Bytes: size, SHA256: hex.EncodeToString(actual.Sum(nil))}, ctx.Err()
}

func TestBackupMonthLifecycle(t *testing.T) {
	rawURL := os.Getenv("TRANSIT_BACKUP_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("restore a backup to a transit_backup_rehearsal_* DB and set TRANSIT_BACKUP_TEST_DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "transit_backup_rehearsal_") {
		t.Fatal("refusing a database without the isolated rehearsal prefix")
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	dir := os.Getenv("TRANSIT_BACKUP_TEST_ARTIFACTS")
	if dir == "" {
		t.Fatal("TRANSIT_BACKUP_TEST_ARTIFACTS is required")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	record := func(name string, value any) {
		t.Helper()
		b, err := json.MarshalIndent(value, "", "  ")
		check(err)
		check(os.WriteFile(filepath.Join(dir, name+".json"), append(b, '\n'), 0600))
	}
	exec := func(sql string, args ...any) { t.Helper(); _, err := db.Exec(ctx, sql, args...); check(err) }
	var version int
	var dirty bool
	check(db.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty))
	if dirty {
		t.Fatal("backup has dirty migration state")
	}
	t.Logf("restored database %s, migration %d", cfg.ConnConfig.Database, version)
	// Bring the old snapshot up to the schema immediately before partitioning.
	migrate := func(target int) {
		t.Helper()
		files, err := filepath.Glob("../../migrations/*.up.sql")
		check(err)
		for _, file := range files {
			v, err := strconv.Atoi(filepath.Base(file)[:6])
			check(err)
			if v <= version || v > target {
				continue
			}
			started := time.Now()
			b, err := os.ReadFile(file)
			check(err)
			tx, err := db.Begin(ctx)
			check(err)
			_, err = tx.Exec(ctx, string(b))
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE schema_migrations SET version=$1,dirty=false`, v)
			}
			if err != nil {
				_ = tx.Rollback(ctx)
				t.Fatalf("migration %d: %v", v, err)
			}
			check(tx.Commit(ctx))
			version = v
			t.Logf("migration %d completed in %s", v, time.Since(started).Round(time.Millisecond))
		}
	}
	if version >= 30 {
		t.Fatal("rehearsal needs a fresh backup restored before migration 30")
	}

	original := map[string]backupFingerprint{}
	originalColumns := map[string]string{}
	for _, table := range transitarchive.PartitionTables {
		var exists bool
		check(db.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "transit."+table.Name).Scan(&exists))
		if !exists {
			continue
		}
		var cols string
		check(db.QueryRow(ctx, `SELECT string_agg(quote_ident(attname),',' ORDER BY attnum) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped`, "transit."+table.Name).Scan(&cols))
		originalColumns[table.Name] = cols
		original[table.Name], err = backupFingerprintTable(ctx, db, "(SELECT "+cols+" FROM "+pgx.Identifier{"transit", table.Name}.Sanitize()+")", "TRUE")
		check(err)
		t.Logf("original backup fingerprint: %s (%d rows)", table.Name, original[table.Name].Rows)
	}
	record("original-backup", original)
	migrate(29)
	baseline := map[string]backupFingerprint{}
	for _, table := range transitarchive.PartitionTables {
		started := time.Now()
		f, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit", table.Name}.Sanitize(), "TRUE")
		check(err)
		baseline[table.Name] = f
		t.Logf("baseline %s: %d rows (%s)", table.Name, f.Rows, time.Since(started).Round(time.Millisecond))
	}
	record("before-partitioning", baseline)
	migrate(31)
	for _, table := range transitarchive.PartitionTables {
		f, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit", table.Name}.Sanitize(), "TRUE")
		check(err)
		if f != baseline[table.Name] {
			t.Fatalf("partition conversion changed %s: before=%+v after=%+v", table.Name, baseline[table.Name], f)
		}
		t.Logf("partition fingerprint preserved: %s", table.Name)
	}
	record("partition-reconciliation", baseline)
	for table, cols := range originalColumns {
		query := "SELECT " + cols + " FROM " + pgx.Identifier{"transit", table}.Sanitize()
		if table == "stop_visit" {
			query += " UNION ALL SELECT " + cols + " FROM transit.stop_visit_duplicate_archive"
		}
		f, err := backupFingerprintTable(ctx, db, "("+query+")", "TRUE")
		check(err)
		if f != original[table] {
			t.Fatalf("schema upgrade changed original raw fields in %s: before=%+v after=%+v", table, original[table], f)
		}
		t.Logf("original backup columns preserved: %s", table)
	}
	record("original-column-reconciliation", original)
	// Independent UTC bounds for August (EDT), not the production predicate.
	boundaryCounts := map[string]int64{}
	for _, table := range []string{"vehicle_position", "alert"} {
		name := pgx.Identifier{"transit", table}.Sanitize()
		direct, err := backupFingerprintTable(ctx, db, name, "feed_timestamp >= '2026-08-01 08:00:00+00'::timestamptz AND feed_timestamp < '2026-09-01 08:00:00+00'::timestamptz")
		check(err)
		partition, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit", table + "_p202608"}.Sanitize(), "TRUE")
		check(err)
		if direct != partition {
			t.Fatalf("independent 4am ownership mismatch for %s", table)
		}
		var n int64
		check(db.QueryRow(ctx, "SELECT count(*) FROM "+name+" WHERE feed_timestamp >= '2026-09-01 04:00:00+00'::timestamptz AND feed_timestamp < '2026-09-01 08:00:00+00'::timestamptz").Scan(&n))
		boundaryCounts[table+"_sept_1_before_4am"] = n
	}
	record("independent-boundary-check", boundaryCounts)
	t.Logf("independent 4am boundary checks passed: %v", boundaryCounts)

	// Slice ONLY this disposable copy to August + September. This keeps the
	// end-to-end rebuild realistic without repeating every older monthly rebuild.
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, transit.TZ)
	rows, err := db.Query(ctx, `SELECT partition_name FROM transit.raw_partition WHERE month < $1::date ORDER BY partition_name`, month)
	check(err)
	var obsolete []string
	for rows.Next() {
		var name string
		check(rows.Scan(&name))
		obsolete = append(obsolete, name)
	}
	check(rows.Err())
	rows.Close()
	for _, name := range obsolete {
		exec("DROP TABLE " + pgx.Identifier{"transit", name}.Sanitize() + " RESTRICT")
	}
	exec(`DELETE FROM transit.raw_partition WHERE month < $1::date`, month)
	for _, table := range []string{"route_band_chunk", "route_day_summary", "metric_rebuild"} {
		exec("DELETE FROM transit."+table+" WHERE date < $1::date", month)
	}
	var schedules []struct{ Dir, Source, At string }
	b, err := os.ReadFile("../../data/metric-validation/schedules.json")
	check(err)
	check(json.Unmarshal(b, &schedules))
	for _, s := range schedules {
		at, err := time.Parse(time.RFC3339, s.At)
		check(err)
		_, err = transit.ImportMetricSchedule(ctx, db, filepath.Join("../..", s.Dir), s.Source, at)
		check(err)
	}
	t.Log("historical timetable versions imported; real-data slice ready")
	owned, retained := map[string]backupFingerprint{}, map[string]backupFingerprint{}
	for _, table := range transitarchive.PartitionTables {
		// Passages are derived during finalization and captured separately below.
		if table.Name == "metric_passage" {
			continue
		}
		name := pgx.Identifier{"transit", table.Name}.Sanitize()
		pred := transitarchive.PartitionPredicate(table, month)
		owned[table.Name], err = backupFingerprintTable(ctx, db, name, pred)
		check(err)
		retained[table.Name], err = backupFingerprintTable(ctx, db, name, "NOT ("+pred+") OR "+pgx.Identifier{table.Key}.Sanitize()+" IS NULL")
		check(err)
	}
	record("august-raw", owned)
	record("outside-august", retained)
	store := &diskRehearsalStore{root: filepath.Join(dir, "objects"), failReport: true}
	w := New(db, store)
	before := time.Date(2026, 9, 1, 3, 59, 59, 0, transit.TZ)
	check(w.RunOnce(ctx, before))
	var n int
	check(db.QueryRow(ctx, `SELECT count(*) FROM transit.archive_month`).Scan(&n))
	if n != 0 {
		t.Fatal("closed month before 4am")
	}
	started := time.Now()
	t.Log("starting real August finalization, with a late upload failure injected")
	err = w.RunOnce(ctx, before.Add(time.Second))
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("expected injected upload failure, got %v", err)
	}
	t.Logf("finalized + tested failed upload in %s", time.Since(started).Round(time.Millisecond))
	var state string
	check(db.QueryRow(ctx, `SELECT state FROM transit.archive_month WHERE month=$1`, month).Scan(&state))
	if state != "frozen" {
		t.Fatal(state)
	}
	check(db.QueryRow(ctx, `SELECT count(*) FROM transit.archive_month WHERE report_base_url IS NOT NULL`).Scan(&n))
	if n != 0 {
		t.Fatal("partial report release was published")
	}
	for _, table := range transitarchive.PartitionTables {
		if table.Name == "metric_passage" {
			continue
		}
		f, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit", table.Name}.Sanitize(), transitarchive.PartitionPredicate(table, month))
		check(err)
		if f != owned[table.Name] {
			t.Fatalf("raw changed on upload failure: %s", table.Name)
		}
	}
	summaries, err := backupFingerprintTable(ctx, db, "transit.route_day_summary", "TRUE")
	check(err)
	bands, err := backupFingerprintTable(ctx, db, "transit.route_band_chunk", "TRUE")
	check(err)
	record("finalized-summaries", map[string]backupFingerprint{"daily": summaries, "bands": bands})
	var expectedMonthly, expectedDaily bytes.Buffer
	reportConn, err := db.Acquire(ctx)
	check(err)
	err = transit.WriteReportsBefore(ctx, reportConn.Conn(), &expectedMonthly, &expectedDaily, month.AddDate(0, 1, 0))
	reportConn.Release()
	check(err)
	expectedReports := map[string][]byte{transit.MonthlyReportFilename(): expectedMonthly.Bytes(), transit.DailyReportFilename(): expectedDaily.Bytes()}
	reportHashes := map[string]string{}
	for name, data := range expectedReports {
		sum := sha256.Sum256(data)
		reportHashes[name] = hex.EncodeToString(sum[:])
	}
	record("expected-report-checksums", reportHashes)
	owned["metric_passage"], err = backupFingerprintTable(ctx, db, "transit.metric_passage", "date >= '2026-08-01' AND date < '2026-09-01'")
	check(err)
	retained["metric_passage"], err = backupFingerprintTable(ctx, db, "transit.metric_passage", "date < '2026-08-01' OR date >= '2026-09-01'")
	check(err)
	record("finalized-august-raw", owned)
	record("finalized-outside-august", retained)

	// Force cleanup to fail after publication, then prove its retry is upload-free.
	exec(`CREATE VIEW transit.rehearsal_cleanup_blocker AS SELECT * FROM transit.vehicle_position_p202608`)
	store.failReport = false
	t.Log("retrying upload, with an eviction dependency injected")
	err = w.RunOnce(ctx, before.Add(time.Second))
	if err == nil {
		t.Fatal("eviction blocker ignored")
	}
	check(db.QueryRow(ctx, `SELECT state FROM transit.archive_month WHERE month=$1`, month).Scan(&state))
	if state != "published" {
		t.Fatalf("expected published, got %s: %v", state, err)
	}
	var reportBase string
	check(db.QueryRow(ctx, `SELECT report_base_url FROM transit.archive_month WHERE month=$1`, month).Scan(&reportBase))
	bundle, err := os.ReadFile(filepath.Join(store.root, strings.TrimPrefix(reportBase, "https://rehearsal.invalid/"), transit.ReportBundleFilename()))
	check(err)
	for name, want := range expectedReports {
		if !bytes.Equal(reportEntries(t, bundle)[name], want) {
			t.Fatalf("published report differs: %s", name)
		}
	}

	calls := store.calls
	exec(`DROP VIEW transit.rehearsal_cleanup_blocker`)
	check(w.RunOnce(ctx, before.Add(time.Second)))
	if calls != store.calls {
		t.Fatal("eviction retry uploaded again")
	}
	check(db.QueryRow(ctx, `SELECT state FROM transit.archive_month WHERE month=$1`, month).Scan(&state))
	if state != "complete" {
		t.Fatal(state)
	}
	for _, table := range transitarchive.PartitionTables {
		name := pgx.Identifier{"transit", table.Name}.Sanitize()
		pred := transitarchive.PartitionPredicate(table, month)
		f, err := backupFingerprintTable(ctx, db, name, pred)
		check(err)
		if f.Rows != 0 {
			t.Fatalf("owned raw still present: %s", table.Name)
		}
		f, err = backupFingerprintTable(ctx, db, name, "NOT ("+pred+") OR "+pgx.Identifier{table.Key}.Sanitize()+" IS NULL")
		check(err)
		if f != retained[table.Name] {
			t.Fatalf("adjacent month changed: %s", table.Name)
		}
	}
	for table, want := range map[string]backupFingerprint{"route_day_summary": summaries, "route_band_chunk": bands} {
		f, err := backupFingerprintTable(ctx, db, "transit."+table, "TRUE")
		check(err)
		if f != want {
			t.Fatalf("retained summaries changed: %s", table)
		}
	}
	var rawPrefix string
	check(db.QueryRow(ctx, `SELECT raw_prefix FROM transit.archive_month WHERE month=$1`, month).Scan(&rawPrefix))
	archiveDir := filepath.Join(store.root, strings.TrimPrefix(rawPrefix, "https://rehearsal.invalid/"))
	t.Log("restoring the successfully published raw evidence into an analysis schema")
	conn, err := db.Acquire(ctx)
	check(err)
	err = transitarchive.Restore(ctx, conn.Conn(), archiveDir, "transit_archive_backup_rehearsal")
	conn.Release()
	check(err)
	for table, want := range owned {
		f, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit_archive_backup_rehearsal", "transit__" + table}.Sanitize(), "TRUE")
		check(err)
		if f != want {
			t.Fatalf("restored raw differs: %s before=%+v after=%+v", table, want, f)
		}
	}
	check(w.RunOnce(ctx, before.Add(time.Second)))
	if calls != store.calls {
		t.Fatal("completed close was not idempotent")
	}
	record("result", map[string]any{"database": cfg.ConnConfig.Database, "month": "2026-08", "state": state, "raw_tables": owned, "retained_tables": retained, "daily_summaries": summaries.Rows, "band_summaries": bands.Rows, "upload_calls": store.calls, "restored": true, "duration_seconds": time.Since(started).Seconds()})
	t.Logf("PASS: partition reconciliation, 4am boundary, real finalization, partial upload failure, atomic publication, eviction retry, adjacent-month fingerprints, row-count/fingerprint-equivalent raw restore; artifacts=%s", dir)
}
