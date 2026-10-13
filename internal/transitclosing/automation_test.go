package transitclosing

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"thundercitizen/internal/transit"
	"thundercitizen/internal/transitarchive"
)

func TestClosingThirteenMonthsAcrossRestartsAndMissedWake(t *testing.T) {
	db := closingDB(t)
	ctx := t.Context()
	for i := 0; i < 14; i++ {
		date := time.Date(2025, 8, 1, 0, 0, 0, 0, transit.TZ).AddDate(0, i, 0)
		if _, err := db.Exec(ctx, `INSERT INTO transit.metric_service(schedule_id,service_id,date) VALUES('schedule','weekday',$1) ON CONFLICT DO NOTHING`, date); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = transitarchive.EnsureMonthPartitions(ctx, conn.Conn(), time.Date(2025, 8, 1, 0, 0, 0, 0, transit.TZ))
	conn.Release()
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{}
	var firstKey string
	var firstBytes []byte
	for i := 0; i < 13; i++ {
		if i == 5 {
			continue
		} // Server misses a boundary; next boot must catch up both.
		now := septemberBoundary().AddDate(-1, i, 0)
		worker := New(db, store) // Every cycle starts without in-memory history.
		worker.finalize = summaryFinalizer(db)
		if err := worker.RunOnce(ctx, now); err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		svc := transit.NewService(db, nil)
		url, err := svc.PublishedReportDownloadURL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		key := strings.TrimPrefix(url, "https://reports.example.test/")
		csvData := reportEntries(t, store.files[key])[transit.MonthlyReportFilename()]
		rows, err := csv.NewReader(bytes.NewReader(csvData)).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		want := i + 1
		if want > 12 {
			want = 12
		}
		if len(rows) != want+1 {
			t.Fatalf("cycle %d: got %d months want %d", i, len(rows)-1, want)
		}
		if i == 0 {
			firstKey = key
			firstBytes = append([]byte(nil), store.files[key]...)
		}
		calls := store.calls
		if err := worker.RunOnce(ctx, now); err != nil {
			t.Fatal(err)
		}
		if calls != store.calls {
			t.Fatal("completed cycle uploaded again")
		}
	}
	if !bytes.Equal(firstBytes, store.files[firstKey]) {
		t.Fatal("older archived ZIP overwritten")
	}
	var summaries, complete int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM transit.route_day_summary`).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM transit.archive_month WHERE state='complete'`).Scan(&complete); err != nil {
		t.Fatal(err)
	}
	if summaries != 13 || complete != 13 {
		t.Fatalf("retained summaries=%d completed=%d", summaries, complete)
	}
	svc := transit.NewService(db, nil)
	url, err := svc.PublishedReportDownloadURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	latest := reportEntries(t, store.files[strings.TrimPrefix(url, "https://reports.example.test/")])[transit.MonthlyReportFilename()]
	if strings.Contains(string(latest), "2025-08") || !strings.Contains(string(latest), "2025-09") || !strings.Contains(string(latest), "2026-08") {
		t.Fatal(string(latest))
	}
	// Recorded artifact version wins over the running binary's filename helper.
	if _, err := db.Exec(ctx, `UPDATE transit.archive_month SET report_manifest=replace(report_manifest::text,'transit_reports_v1.zip','transit_reports_v0.zip')::jsonb WHERE month='2026-08-01'`); err != nil {
		t.Fatal(err)
	}
	url, err = svc.PublishedReportDownloadURL(ctx)
	if err != nil || !strings.HasSuffix(url, "/transit_reports_v0.zip") {
		t.Fatalf("recorded artifact ignored: %s %v", url, err)
	}
}

func TestClosingWakeUsesLocalFourAMAcrossDST(t *testing.T) {
	for _, day := range []string{"2026-03-08", "2026-11-01"} {
		date, err := time.ParseInLocation("2006-01-02", day, transit.TZ)
		if err != nil {
			t.Fatal(err)
		}
		before := time.Date(date.Year(), date.Month(), date.Day(), 3, 59, 0, 0, transit.TZ)
		want := time.Date(date.Year(), date.Month(), date.Day(), 4, 0, 0, 0, transit.TZ)
		if got := nextWake(before); !got.Equal(want) {
			t.Fatalf("%s next wake %s", day, got)
		}
		if got := nextWake(want); !got.Equal(want.Add(5 * time.Minute)) {
			t.Fatal(got)
		}
	}
}
