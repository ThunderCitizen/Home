package transitclosing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"thundercitizen/internal/transit"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"thundercitizen/internal/transitarchive"
)

// Explicitly opt in: uploads real backup evidence to a unique remote prefix.
// Leaves remote objects for operator inspection; never deletes bucket objects.
func TestSpacesBackupRehearsal(t *testing.T) {
	if os.Getenv("TRANSIT_SPACES_REHEARSAL") != "1" {
		t.Skip("real Spaces rehearsal is opt-in")
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	root := os.Getenv("TRANSIT_BACKUP_TEST_ARTIFACTS")
	source := os.Getenv("TRANSIT_SPACES_REHEARSAL_SOURCE")
	if root == "" || source == "" {
		t.Fatal("backup artifact root and source archive directory required")
	}
	m, err := transitarchive.Verify(source)
	check(err)
	store, err := StoreFromConfig()
	check(err)
	s, ok := store.(*Spaces)
	if !ok {
		t.Fatal("Spaces configuration required")
	}
	// Require the known bucket explicitly; no inherited deployment destination.
	if s.bucket != "thundercitizen" {
		t.Fatal("unexpected rehearsal bucket")
	}
	db := closingDB(t)
	ctx := t.Context()
	prefix := "transit/rehearsals/" + time.Now().UTC().Format("20060102T150405.000000000Z")
	out := filepath.Join(root, "spaces-"+filepath.Base(prefix))
	check(os.MkdirAll(out, 0700))
	t.Logf("remote prefix: %s; local evidence: %s", s.PublicURL(prefix), out)
	check(os.WriteFile(filepath.Join(out, "prefix.txt"), []byte(s.PublicURL(prefix)+"\n"), 0600))
	client := &http.Client{Timeout: 60 * time.Second}
	anonymous := func(key string, want int, data []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.PublicURL(key), nil)
		check(err)
		resp, err := client.Do(req)
		check(err)
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("anonymous access %s: status %d, want %d", key, resp.StatusCode, want)
		}
		if data != nil {
			got, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(data)+1)))
			check(err)
			if !bytes.Equal(got, data) {
				t.Fatalf("public bytes differ: %s", key)
			}
		}
	}
	// Check private-object behavior with harmless content before uploading raw data.
	probe := filepath.Join(out, "probe.txt")
	check(os.WriteFile(probe, []byte("private transit rehearsal probe\n"), 0600))
	_, err = s.UploadVerified(ctx, prefix+"/private-probe.txt", probe, false)
	check(err)
	anonymous(prefix+"/private-probe.txt", http.StatusForbidden, nil)
	t.Log("authenticated upload/read-back and anonymous private-object denial passed")
	download := filepath.Join(out, "downloaded")
	check(os.MkdirAll(download, 0700))
	var uploaded []Object
	names := make([]string, 0, len(m.Tables)+1)
	for _, table := range m.Tables {
		names = append(names, table.File)
	}
	names = append(names, "manifest.json")
	for _, name := range names {
		key := prefix + "/raw/" + name
		obj, err := s.UploadVerified(ctx, key, filepath.Join(source, name), false)
		check(err)
		uploaded = append(uploaded, obj)
		check(s.client.FGetObject(ctx, s.bucket, key, filepath.Join(download, name), minio.GetObjectOptions{}))
		anonymous(key, http.StatusForbidden, nil)
		t.Logf("verified private upload/download: %s (%d bytes)", name, obj.Bytes)
	}
	_, err = transitarchive.Verify(download)
	check(err)
	reportDir := os.Getenv("TRANSIT_SPACES_REHEARSAL_REPORTS")
	if reportDir == "" {
		t.Fatal("source report directory required")
	}
	var expected map[string]string
	b, err := os.ReadFile(filepath.Join(root, "expected-report-checksums.json"))
	check(err)
	check(json.Unmarshal(b, &expected))
	// Previously recorded backup rehearsals may predate versioned filenames.
	if len(expected) != 2 {
		t.Fatal("expected exactly two recorded report checksums")
	}
	for name := range expected {
		if name != transit.MonthlyReportFilename() && name != transit.DailyReportFilename() && name != "monthly_metrics.csv" && name != "route_day_summaries.csv" {
			t.Fatalf("unexpected recorded report filename: %s", name)
		}
		file := filepath.Join(reportDir, name)
		b, err := os.ReadFile(file)
		check(err)
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != expected[name] {
			t.Fatalf("source report checksum mismatch: %s", name)
		}
		key := prefix + "/reports/" + name
		obj, err := s.UploadVerified(ctx, key, file, true)
		check(err)
		uploaded = append(uploaded, obj)
		anonymous(key, http.StatusOK, b)
		t.Logf("verified anonymous CSV download: %s", s.PublicURL(key))
	}
	t.Log("restoring downloaded bucket evidence into disposable analysis database")
	conn, err := db.Acquire(ctx)
	check(err)
	err = transitarchive.Restore(ctx, conn.Conn(), download, "transit_archive_spaces_rehearsal")
	conn.Release()
	check(err)
	var previous struct {
		RawTables map[string]backupFingerprint `json:"raw_tables"`
	}
	b, err = os.ReadFile(filepath.Join(root, "result.json"))
	check(err)
	check(json.Unmarshal(b, &previous))
	for table, want := range previous.RawTables {
		got, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit_archive_spaces_rehearsal", "transit__" + table}.Sanitize(), "TRUE")
		check(err)
		if got != want {
			t.Fatalf("restored backup mismatch: %s got=%+v want=%+v", table, got, want)
		}
		t.Logf("restored fingerprint preserved: %s (%d rows)", table, got.Rows)
	}
	b, err = json.MarshalIndent(map[string]any{"prefix": s.PublicURL(prefix), "objects": uploaded, "raw_tables": previous.RawTables, "restored": true, "private_anonymous_denied": true, "public_csv_bytes_verified": true}, "", "  ")
	check(err)
	check(os.WriteFile(filepath.Join(out, "result.json"), append(b, '\n'), 0600))
	t.Logf("PASS; remote objects retained for operator cleanup: %s", s.PublicURL(prefix))
}
