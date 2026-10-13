package transitarchive

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoundsGraceAndDST(t *testing.T) {
	loc, _ := time.LoadLocation("America/Thunder_Bay")
	// November starts in daylight time; the grace interval is elapsed time.
	boundary := time.Date(2026, 11, 1, 4, 0, 0, 0, loc).Add(48 * time.Hour)
	if _, _, err := Bounds("2026-10", boundary.Add(-time.Second), 48*time.Hour); err == nil {
		t.Fatal("accepted before grace")
	}
	start, gotEnd, err := Bounds("2026-10", boundary, 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if start.Format("2006-01-02") != "2026-10-01" || gotEnd.Format("2006-01-02") != "2026-11-01" {
		t.Fatal(start, gotEnd)
	}
	if _, _, err := Bounds("2026-10", boundary, -time.Hour); err == nil {
		t.Fatal("accepted negative grace")
	}
}
func fixture(t *testing.T) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	file := "transit__alert.csv.gz"
	f, err := os.Create(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	// Embedded newline and quote must count as one record, including NULL vs empty.
	_, err = gz.Write([]byte("1,\"multi\nline \"\"quoted\"\"\"\n2,\\N\n3,\"\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, _ := os.ReadFile(filepath.Join(dir, file))
	sum := sha256.Sum256(b)
	m := Manifest{Version: 1, Month: "2026-09", Tables: []Table{{Schema: "transit", Name: "alert", Columns: []Column{{Name: "id", Type: "bigint"}, {Name: "description", Type: "text"}}, File: file, SHA256: hex.EncodeToString(sum[:]), Rows: 3}}}
	saveManifest(t, dir, m)
	return dir, m
}
func saveManifest(t *testing.T, dir string, m Manifest) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestVerifyMultilineCSV(t *testing.T) {
	dir, _ := fixture(t)
	if _, err := Verify(dir); err != nil {
		t.Fatal(err)
	}
}
func TestVerifyRejectsTampering(t *testing.T) {
	for _, kind := range []string{"rows", "hash", "path", "duplicate", "width", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			dir, m := fixture(t)
			switch kind {
			case "rows":
				m.Tables[0].Rows++
			case "hash":
				m.Tables[0].SHA256 = strings.Repeat("0", 64)
			case "path":
				m.Tables[0].File = "../payload.csv.gz"
			case "duplicate":
				m.Tables = append(m.Tables, m.Tables[0])
			case "width":
				m.Tables[0].Columns = m.Tables[0].Columns[:1]
			case "truncated":
				p := filepath.Join(dir, m.Tables[0].File)
				b, _ := os.ReadFile(p)
				os.WriteFile(p, b[:len(b)-5], 0600)
			}
			saveManifest(t, dir, m)
			if _, err := Verify(dir); err == nil {
				t.Fatal("accepted damaged archive")
			}
		})
	}
}
func TestPredicatesPreserveEvidence(t *testing.T) {
	start, _ := time.Parse("2006-01-02", "2026-09-01")
	end := start.AddDate(0, 1, 0)
	if got := predicate("metric_schedule", start, end); got != "TRUE" {
		t.Fatal(got)
	}
	gps := predicate("vehicle_position", start, end)
	if !strings.Contains(gps, "2026-08-31") || !strings.Contains(gps, "2026-10-02") {
		t.Fatal(gps)
	}
	cancels := predicate("cancellation", start, end)
	if !strings.Contains(cancels, "start_date") || !strings.Contains(cancels, "OR (feed_timestamp") {
		t.Fatal(cancels)
	}
}
