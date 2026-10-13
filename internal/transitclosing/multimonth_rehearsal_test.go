package transitclosing

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/transit"
	"thundercitizen/templates/pages"
)

type rehearsalPrefixStore struct {
	Store
	prefix string
	calls  int
}

func (s *rehearsalPrefixStore) PublicURL(key string) string {
	return s.Store.PublicURL(s.prefix + "/" + strings.TrimPrefix(key, "transit/"))
}
func (s *rehearsalPrefixStore) UploadVerified(ctx context.Context, key, file string, public bool) (Object, error) {
	s.calls++
	return s.Store.UploadVerified(ctx, s.prefix+"/"+strings.TrimPrefix(key, "transit/"), file, public)
}

// Real July/August backup slice, real finalizer, real Spaces uploads, and Method
// page HTTP handling. Explicitly gated; retains remote files for manual cleanup.
func TestSpacesMultiMonthRehearsal(t *testing.T) {
	if os.Getenv("TRANSIT_SPACES_MULTIMONTH") != "1" {
		t.Skip("real multi-month bucket rehearsal is opt-in")
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("TRANSIT_BACKUP_TEST_DATABASE_URL"))
	check(err)
	if !strings.HasPrefix(cfg.ConnConfig.Database, "transit_backup_rehearsal_") {
		t.Fatal("isolated backup DB required")
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	root := os.Getenv("TRANSIT_BACKUP_TEST_ARTIFACTS")
	if root == "" {
		t.Fatal("artifact directory required")
	}
	check(os.MkdirAll(root, 0700))
	ctx := t.Context()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	check(err)
	defer db.Close()
	var version int
	var dirty bool
	check(db.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty))
	if dirty {
		t.Fatal("dirty migration state")
	}
	files, err := filepath.Glob("../../migrations/*.up.sql")
	check(err)
	for _, file := range files {
		v, err := strconv.Atoi(filepath.Base(file)[:6])
		check(err)
		if v <= version || v > 31 {
			continue
		}
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
			t.Fatal(err)
		}
		check(tx.Commit(ctx))
		version = v
		t.Logf("migration %d complete", v)
	}
	// Retain only this scenario's derived days; raw COPY was independently sliced.
	for _, table := range []string{"route_band_chunk", "route_day_summary", "metric_rebuild"} {
		_, err = db.Exec(ctx, "DELETE FROM transit."+table+" WHERE date < '2026-07-01' OR date >= '2026-09-01'")
		check(err)
	}
	check(transit.ImportHistoricalSchedules(ctx, db))

	base, err := StoreFromConfig()
	check(err)
	spaces, ok := base.(*Spaces)
	if !ok || spaces.bucket != "thundercitizen" {
		t.Fatal("expected rehearsal Spaces bucket")
	}
	prefix := "transit/rehearsals/multimonth-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	// A retry must keep the same destination as the durable raw checkpoints.
	if saved, err := os.ReadFile(filepath.Join(root, "prefix.txt")); err == nil {
		url := strings.TrimSpace(string(saved))
		if !strings.HasPrefix(url, spaces.publicBase+"/transit/rehearsals/") {
			t.Fatal("unexpected saved rehearsal prefix")
		}
		prefix = strings.TrimPrefix(url, spaces.publicBase+"/")
	}
	s := &rehearsalPrefixStore{Store: base, prefix: prefix}
	check(os.WriteFile(filepath.Join(root, "prefix.txt"), []byte(base.PublicURL(prefix)+"\n"), 0600))
	t.Logf("remote prefix: %s", base.PublicURL(prefix))
	// The same production worker catches up both completed months, oldest first.
	w := New(db, s)
	t.Log("closing July and August with real production finalizer and Spaces store")
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, transit.TZ)
	check(w.RunOnce(ctx, now))
	var complete int
	check(db.QueryRow(ctx, `SELECT count(*) FROM transit.archive_month WHERE month IN ('2026-07-01','2026-08-01') AND state='complete'`).Scan(&complete))
	if complete != 2 {
		t.Fatalf("completed months=%d", complete)
	}
	service := transit.NewService(db, nil)
	latest, err := service.PublishedReportBaseURL(ctx)
	check(err)
	var august string
	check(db.QueryRow(ctx, `SELECT report_base_url FROM transit.archive_month WHERE month='2026-08-01'`).Scan(&august))
	if latest != august {
		t.Fatal("Method did not select latest published release")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	download := func(url string) []byte {
		t.Helper()
		name := filepath.Base(url)
		bundleURL := strings.TrimSuffix(url, name) + transit.ReportBundleFilename()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, bundleURL, nil)
		check(err)
		resp, err := client.Do(req)
		check(err)
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("download %s status %d", url, resp.StatusCode)
		}
		if resp.Header.Get("Content-Type") != "application/zip" || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment;") {
			t.Fatal("missing CSV download headers")
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		check(err)
		return reportEntries(t, b)[name]
	}
	var expectedMonthly, expectedDaily bytes.Buffer
	conn, err := db.Acquire(ctx)
	check(err)
	err = transit.WriteReportsBefore(ctx, conn.Conn(), &expectedMonthly, &expectedDaily, time.Date(2026, 9, 1, 0, 0, 0, 0, transit.TZ))
	conn.Release()
	check(err)
	counts := map[string]int{}
	var dailyRecords [][]string
	for name, want := range map[string][]byte{transit.MonthlyReportFilename(): expectedMonthly.Bytes(), transit.DailyReportFilename(): expectedDaily.Bytes()} {
		data := download(latest + "/" + name)
		if !bytes.Equal(data, want) {
			t.Fatalf("published CSV differs from retained summaries: %s", name)
		}
		check(os.WriteFile(filepath.Join(root, name), data, 0600))
		records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
		check(err)
		counts[name] = len(records) - 1
		if name == transit.MonthlyReportFilename() {
			if len(records) != 3 || records[1][0] != "2026-07" || records[2][0] != "2026-08" {
				t.Fatalf("unexpected monthly rows: %v", records)
			}
		} else {
			dailyRecords = records
		}
	}
	months := map[string]int{}
	days := map[string]bool{}
	unique := map[string]bool{}
	for _, row := range dailyRecords[1:] {
		day, route := row[0], row[1]
		if len(day) != 10 || (day[:7] != "2026-07" && day[:7] != "2026-08") {
			t.Fatalf("unexpected date %s", day)
		}
		key := day + "/" + route
		if unique[key] {
			t.Fatalf("duplicate route/day %s", key)
		}
		unique[key] = true
		months[day[:7]]++
		days[day] = true
	}
	if len(days) != 62 || months["2026-07"] == 0 || months["2026-08"] == 0 {
		t.Fatalf("missing service days/months: %d %v", len(days), months)
	}
	// Also verify the July immutable release was not overwritten by August.
	var july string
	check(db.QueryRow(ctx, `SELECT report_base_url FROM transit.archive_month WHERE month='2026-07-01'`).Scan(&july))
	julyRows, err := csv.NewReader(bytes.NewReader(download(july + "/" + transit.MonthlyReportFilename()))).ReadAll()
	check(err)
	if len(julyRows) != 2 || julyRows[1][0] != "2026-07" {
		t.Fatal("July release changed")
	}
	handler := transit.NewHandler(db, transit.Renderer{TransitMethod: func(vm transit.MethodViewModel) transit.RenderFunc { return pages.TransitMethod(vm).Render }}, nil)
	rr := httptest.NewRecorder()
	handler.PageRoutes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/method", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("Method HTTP %d", rr.Code)
	}
	for _, name := range []string{transit.ReportBundleFilename()} {
		if !strings.Contains(rr.Body.String(), `href="`+latest+"/"+name+`"`) {
			t.Fatalf("Method link missing: %s", name)
		}
	}
	check(os.WriteFile(filepath.Join(root, "method.html"), rr.Body.Bytes(), 0600))
	calls := s.calls
	check(w.RunOnce(ctx, now))
	if s.calls != calls {
		t.Fatal("completed months uploaded again")
	}
	b, err := json.MarshalIndent(map[string]any{"prefix": base.PublicURL(prefix), "latest_reports": latest, "july_reports": july, "completed_months": complete, "report_rows": counts, "daily_rows_by_month": months, "service_days": len(days), "method_http_status": rr.Code, "upload_calls": s.calls, "idempotent": true}, "", "  ")
	check(err)
	check(os.WriteFile(filepath.Join(root, "result.json"), append(b, '\n'), 0600))
	t.Logf("PASS: two closed months, 62 days, %v daily rows; reports=%s", months, latest)
	fmt.Fprintln(os.Stdout, "Remote rehearsal files retained for operator cleanup:", base.PublicURL(prefix))
}
