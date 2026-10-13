package transit

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"thundercitizen/internal/transit/chunk"
	"time"
)

func TestConsolidatedReportReduction(t *testing.T) {
	rows := []chunk.ChunkView{
		{RouteID: "A", Date: "2026-08-01", Trips: 1, Scheduled: 2, Cancelled: 1, Quality: chunk.Quality{Version: chunk.Version, OTPCount: 1, OTPOnTime: 1}},
		{RouteID: "B", Date: "2026-08-02", Trips: 9, Scheduled: 18, Cancelled: 1, Quality: chunk.Quality{Version: chunk.Version, OTPCount: 9, OTPOnTime: 0}},
	}
	var monthly, daily bytes.Buffer
	if err := writeReportCSVs(&monthly, &daily, rows, map[string]reportRoute{"A": {Number: "1", Name: "Mainline"}}); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(&monthly).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1][5] != "10.0" || records[1][6] != "10" || records[1][9] != "" {
		t.Fatalf("incorrect pooled metrics: %v", records)
	}
	days, err := csv.NewReader(&daily).ReadAll()
	if err != nil || len(days) != 3 {
		t.Fatalf("daily rows: %v %v", days, err)
	}
	if !reflect.DeepEqual(days[0], []string{"Date", "Route", "Route name", "Trips", "Cancelled", "OTP (%)"}) ||
		!reflect.DeepEqual(days[1], []string{"2026-08-01", "1", "Mainline", "1", "1", "100"}) {
		t.Fatalf("public route report does not match Daily fields: %v", days)
	}
}

func TestPublicRouteReportKeepsUnavailableValuesBlank(t *testing.T) {
	var monthly, daily bytes.Buffer
	rows := []chunk.ChunkView{{RouteID: "A", Date: "2026-04-12", Quality: chunk.Quality{Version: chunk.Version}}}
	if err := writeReportCSVs(&monthly, &daily, rows, nil); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(&daily).ReadAll()
	if err != nil || !reflect.DeepEqual(records[1], []string{"2026-04-12", "A", "A", "", "", ""}) {
		t.Fatalf("unavailable data presented as zero: %v %v", records, err)
	}
}
func TestConsolidatedReportsCompletedServiceMonth(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `CREATE TABLE transit.route(route_id text PRIMARY KEY,short_name text,long_name text)`)
	routeQueryExec(t, db, `INSERT INTO transit.route_band_chunk(route_id,date,band) VALUES ('A','2025-08-31','morning'),('A','2025-09-01','morning'),('A','2026-08-01','morning'),('A','2026-09-01','morning'),('A','2026-10-01','morning')`)
	installRouteDaySummary(t, db)
	routeQueryExec(t, db, `UPDATE transit.route_day_summary SET is_final=(date < '2026-10-01')`)
	conn, err := db.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var m, d bytes.Buffer
	// At 03:59 on October 1, September is still the current service month.
	now := time.Date(2026, 10, 1, 3, 59, 0, 0, TZ)
	if err := WriteConsolidatedReports(t.Context(), conn.Conn(), &m, &d, now); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.String(), "2026-09") || !strings.Contains(m.String(), "2026-08") || strings.Contains(m.String(), "2025-08") || !strings.Contains(m.String(), "2025-09") {
		t.Fatal(m.String())
	}
	m.Reset()
	d.Reset()
	if err := WriteConsolidatedReports(t.Context(), conn.Conn(), &m, &d, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.String(), "2026-09") || strings.Contains(m.String(), "2026-10") || strings.Contains(m.String(), "2025-09") {
		t.Fatal(m.String())
	}
	routeQueryExec(t, db, `UPDATE transit.route_day_summary SET is_final=false WHERE date='2026-08-01'`)
	if err := WriteConsolidatedReports(t.Context(), conn.Conn(), &m, &d, now); err == nil {
		t.Fatal("accepted unfinished summary")
	}
}
func TestReportBaseURL(t *testing.T) {
	for _, url := range []string{"", "javascript:alert(1)", "http://example.com", "https://example.com?query=1"} {
		t.Setenv("TRANSIT_REPORT_BASE_URL", url)
		if ReportBaseURL() != "" {
			t.Fatal(url)
		}
	}
	t.Setenv("TRANSIT_REPORT_BASE_URL", "https://reports.example.com/v1/")
	if ReportBaseURL() != "https://reports.example.com/v1" {
		t.Fatal(ReportBaseURL())
	}
}

func TestLegacyDownloadDoesNotQueryDatabase(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transit/download?from=2026-10-01", nil)
	w := httptest.NewRecorder()
	(&Handler{}).dataDownload(w, req)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/transit/method#downloads" {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestReportZIPContainsBothCSVsAndIsDeterministic(t *testing.T) {
	monthly, daily := []byte("Month,Trips\n2026-09,10\n"), []byte("Date,Trips\n2026-09-01,10\n")
	var first, second bytes.Buffer
	for _, out := range []*bytes.Buffer{&first, &second} {
		if err := writeReportZIP(out, monthly, daily); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("bundle changed on retry")
	}
	z, err := zip.NewReader(bytes.NewReader(first.Bytes()), int64(first.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 2 {
		t.Fatal("expected exactly two CSVs")
	}
	expected := map[string][]byte{MonthlyReportFilename(): monthly, DailyReportFilename(): daily}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Equal(b, expected[f.Name]) {
			t.Fatalf("invalid entry %s", f.Name)
		}
	}
}
