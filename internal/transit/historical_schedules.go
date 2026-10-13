package transit

import (
	"archive/zip"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These recovery snapshots ship in the binary, including production images
// whose build context deliberately excludes local data and downloaded GTFS.
//
//go:embed schedules/historical.zip schedules/manifest.json
var historicalSchedules embed.FS

type historicalSchedule struct {
	Dir, Source, ID string
	At              time.Time
}

// ImportHistoricalSchedules restores captured timetables before any historical
// calculation or month close. Existing schedules are untouched; subsequent boots
// only check their content IDs. Capture times preserve timetable selection order.
func ImportHistoricalSchedules(ctx context.Context, db *pgxpool.Pool) error {
	b, err := historicalSchedules.ReadFile("schedules/manifest.json")
	if err != nil {
		return err
	}
	var entries []historicalSchedule
	if err := json.Unmarshal(b, &entries); err != nil {
		return err
	}
	b, err = historicalSchedules.ReadFile("schedules/historical.zip")
	if err != nil {
		return err
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var exists bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM transit.metric_schedule WHERE id=$1)`, entry.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := importHistoricalSchedule(ctx, db, z, entry); err != nil {
			return fmt.Errorf("historical GTFS %s: %w", entry.Source, err)
		}
	}
	return nil
}

func importHistoricalSchedule(ctx context.Context, db *pgxpool.Pool, z *zip.Reader, entry historicalSchedule) error {
	dir, err := os.MkdirTemp("", "transit-historical-gtfs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for _, name := range []string{"trips.txt", "stops.txt", "stop_times.txt", "calendar.txt", "calendar_dates.txt"} {
		b, err := fs.ReadFile(z, entry.Dir+"/"+name)
		if os.IsNotExist(err) && (name == "calendar.txt" || name == "calendar_dates.txt") {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			return err
		}
	}
	id, err := ImportMetricSchedule(ctx, db, dir, entry.Source, entry.At)
	if err != nil {
		return err
	}
	if id != entry.ID {
		return fmt.Errorf("content ID %s does not match manifest %s", id, entry.ID)
	}
	return nil
}
