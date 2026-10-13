package transitclosing

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"thundercitizen/internal/transit"
	"thundercitizen/internal/transitarchive"
)

// Completes the online rehearsal through the actual monthly closer, private
// Spaces archives, public multi-month ZIP, and restore from downloaded evidence.
func TestOnlineBackupBucketLifecycle(t *testing.T) {
	if os.Getenv("TRANSIT_SPACES_MULTIMONTH") != "1" || os.Getenv("TRANSIT_ONLINE_REHEARSAL_DATABASE_URL") == "" {
		t.Skip("explicit online backup and Spaces rehearsal required")
	}
	ctx := t.Context()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("TRANSIT_ONLINE_REHEARSAL_DATABASE_URL"))
	check(err)
	if !strings.HasPrefix(cfg.ConnConfig.Database, "transit_backup_rehearsal_online_") {
		t.Fatal("isolated online backup required")
	}
	root := os.Getenv("TRANSIT_BACKUP_TEST_ARTIFACTS")
	if _, err = os.Stat(filepath.Join(root, "online-migration-result.json")); err != nil {
		t.Fatal("online copy/fingerprint rehearsal must pass first")
	}
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	check(err)
	defer db.Close()
	base, err := StoreFromConfig()
	check(err)
	spaces, ok := base.(*Spaces)
	if !ok || spaces.bucket != "thundercitizen" {
		t.Fatal("expected rehearsal bucket")
	}
	client := &http.Client{Timeout: time.Minute}
	denied := func(url string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		check(err)
		resp, err := client.Do(req)
		check(err)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("private object status %d", resp.StatusCode)
		}
	}
	// A harmless ACL probe precedes any actual evidence upload.
	probe := filepath.Join(root, "private-probe.txt")
	check(os.WriteFile(probe, []byte("private rehearsal probe\n"), 0600))
	probeKey := "transit/rehearsals/online-private-probe-" + time.Now().UTC().Format("20060102T150405Z")
	_, err = spaces.UploadVerified(ctx, probeKey, probe, false)
	check(err)
	denied(spaces.PublicURL(probeKey))
	// Slice only this already-verified scratch copy to July/August/September.
	// September stays live while the two prior months close.
	rows, err := db.Query(ctx, `SELECT partition_name FROM transit.raw_partition WHERE month<'2026-07-01'`)
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
		_, err = db.Exec(ctx, "DROP TABLE "+pgx.Identifier{"transit", name}.Sanitize()+" RESTRICT")
		check(err)
	}
	_, err = db.Exec(ctx, `DELETE FROM transit.raw_partition WHERE month<'2026-07-01'`)
	check(err)
	expected := map[string]backupFingerprint{}
	expectedFile := filepath.Join(root, "august-before-closing.json")
	if b, err := os.ReadFile(expectedFile); err == nil {
		check(json.Unmarshal(b, &expected))
	} else {
		for _, table := range transitarchive.PartitionTables {
			if table.Name == "metric_passage" {
				continue
			}
			f, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit", table.Name}.Sanitize(), transitarchive.PartitionPredicate(table, time.Date(2026, 8, 1, 0, 0, 0, 0, transit.TZ)))
			check(err)
			expected[table.Name] = f
		}
		b, err := json.MarshalIndent(expected, "", "  ")
		check(err)
		check(os.WriteFile(expectedFile, b, 0600))
	}
	t.Setenv("TRANSIT_BACKUP_TEST_DATABASE_URL", os.Getenv("TRANSIT_ONLINE_REHEARSAL_DATABASE_URL"))
	if !t.Run("real_months_and_public_downloads", TestSpacesMultiMonthRehearsal) {
		t.FailNow()
	}
	prefixBytes, err := os.ReadFile(filepath.Join(root, "prefix.txt"))
	check(err)
	prefix := strings.TrimPrefix(strings.TrimSpace(string(prefixBytes)), spaces.publicBase+"/")
	if !strings.HasPrefix(prefix, "transit/rehearsals/") {
		t.Fatal("invalid isolated prefix")
	}
	var rawPrefix string
	check(db.QueryRow(ctx, `SELECT raw_prefix FROM transit.archive_month WHERE month='2026-08-01' AND state='complete'`).Scan(&rawPrefix))
	keyRoot := strings.TrimPrefix(rawPrefix, spaces.publicBase+"/")
	if !strings.HasPrefix(keyRoot, prefix+"/raw/") {
		t.Fatal("archive checkpoint escaped rehearsal prefix")
	}
	download := filepath.Join(root, "downloaded-august")
	check(os.MkdirAll(download, 0700))
	fetch := func(name string) {
		t.Helper()
		key := keyRoot + "/" + name
		denied(spaces.PublicURL(key))
		check(spaces.client.FGetObject(ctx, spaces.bucket, key, filepath.Join(download, name), minio.GetObjectOptions{}))
	}
	fetch("manifest.json")
	b, err := os.ReadFile(filepath.Join(download, "manifest.json"))
	check(err)
	var manifest transitarchive.Manifest
	check(json.Unmarshal(b, &manifest))
	for _, table := range manifest.Tables {
		fetch(table.File)
	}
	_, err = transitarchive.Verify(download)
	check(err)
	conn, err := db.Acquire(ctx)
	check(err)
	err = transitarchive.Restore(ctx, conn.Conn(), download, "transit_archive_online_rehearsal")
	conn.Release()
	check(err)
	for table, want := range expected {
		got, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit_archive_online_rehearsal", "transit__" + table}.Sanitize(), "TRUE")
		check(err)
		if got != want {
			t.Fatalf("restored %s mismatch: %+v vs %+v", table, got, want)
		}
		t.Logf("bucket restore fingerprint preserved: %s (%d rows)", table, got.Rows)
	}
	// Verify the live rehearsal server sees the new DB-backed report pointer.
	if url := os.Getenv("TRANSIT_ONLINE_REHEARSAL_HTTP_URL"); url != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", url+"/transit/method", nil)
		check(err)
		resp, err := client.Do(req)
		check(err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		check(err)
		reportURL, err := transit.NewService(db, nil).PublishedReportDownloadURL(ctx)
		check(err)
		if resp.StatusCode != 200 || !strings.Contains(string(body), reportURL) {
			t.Fatal("running Method page did not pick up published ZIP")
		}
	}
	result := map[string]any{"raw_private": true, "verified_bucket_restore": true, "raw_tables": expected, "public_zip_and_method_verified": true, "prefix": strings.TrimSpace(string(prefixBytes))}
	b, err = json.MarshalIndent(result, "", "  ")
	check(err)
	check(os.WriteFile(filepath.Join(root, "bucket-restore-result.json"), b, 0600))
}
