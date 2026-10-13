package transit

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"testing"
)

func TestBundledHistoricalSchedules(t *testing.T) {
	b, err := historicalSchedules.ReadFile("schedules/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []historicalSchedule
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 8 {
		t.Fatalf("got %d snapshots", len(entries))
	}
	b, err = historicalSchedules.ReadFile("schedules/historical.zip")
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range entries {
		if entry.At.IsZero() || len(entry.Source) != 40 {
			t.Fatalf("missing provenance: %+v", entry)
		}
		if i > 0 && !entry.At.After(entries[i-1].At) {
			t.Fatal("snapshots out of capture order")
		}
		h := sha256.New()
		for _, name := range []string{"trips.txt", "stops.txt", "stop_times.txt", "calendar.txt", "calendar_dates.txt"} {
			b, err := fs.ReadFile(z, entry.Dir+"/"+name)
			if errors.Is(err, fs.ErrNotExist) && (name == "calendar.txt" || name == "calendar_dates.txt") {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(h, name)
			h.Write(b)
		}
		if got := fmt.Sprintf("%x", h.Sum(nil)); got != entry.ID {
			t.Fatalf("%s checksum mismatch", entry.Dir)
		}
	}
}

func TestHistoricalSchedulesImportAcrossRestarts(t *testing.T) {
	db := metricHistoryDB(t)
	for i := 0; i < 2; i++ {
		if err := ImportHistoricalSchedules(t.Context(), db); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRow(t.Context(), `SELECT count(*) FROM transit.metric_schedule`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 8 {
			t.Fatalf("got %d schedules after boot %d", count, i)
		}
	}
}
