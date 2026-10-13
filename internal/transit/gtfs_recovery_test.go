package transit

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestUnchangedFeedRepairsMissingContainerTimetable(t *testing.T) {
	db := metricHistoryDB(t)
	source := metricFixtureSchedule(t)
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	z := zip.NewWriter(&body)
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		w, err := z.Create(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "missing-container-files")
	for i := 0; i < 2; i++ {
		if err := archiveUnchangedGTFS(t.Context(), db, body.Bytes(), dest); err != nil {
			t.Fatal(err)
		}
		var schedules, trips int
		if err := db.QueryRow(t.Context(), `SELECT count(*) FROM transit.metric_schedule`).Scan(&schedules); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(t.Context(), `SELECT count(*) FROM transit.metric_trip`).Scan(&trips); err != nil {
			t.Fatal(err)
		}
		if schedules != 1 || trips != 4 {
			t.Fatalf("schedules=%d trips=%d", schedules, trips)
		}
		if _, err := os.Stat(filepath.Join(dest, "trips.txt")); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(dest); err != nil {
			t.Fatal(err)
		} // Next container restart.
	}
}
