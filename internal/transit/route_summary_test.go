package transit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"thundercitizen/internal/transit/chunk"
)

func TestRouteSummaryIgnoresLegacyAndMissingObservations(t *testing.T) {
	legacy := chunk.ChunkView{RouteID: "1", Trips: 900, Cancelled: 90, Quality: chunk.Quality{Version: 0}}
	missing := chunk.ChunkView{RouteID: "1", Scheduled: 100, Quality: chunk.Quality{Version: chunk.Version}}
	if got := RouteRowKPIFromChunks([]chunk.ChunkView{legacy, missing}, "1"); got.HasData || got.Cancel != "" {
		t.Fatalf("unobserved summaries = %+v", got)
	}
	passageOnly := chunk.ChunkView{RouteID: "1", Quality: chunk.Quality{Version: chunk.Version, WindowSeconds: 60, WaitObservedArea: 3600}}
	if got := RouteRowKPIFromChunks([]chunk.ChunkView{passageOnly}, "1"); got.EWT != "1.0" || got.Cancel != "" || !got.HasData {
		t.Fatalf("GPS-only metric was suppressed or treated as zero cancellations: %+v", got)
	}
	observed := chunk.ChunkView{RouteID: "1", Trips: 10, Scheduled: 20, Cancelled: 2, Quality: chunk.Quality{Version: chunk.Version, OTPCount: 20, OTPOnTime: 15}}
	got := RouteRowKPIFromChunks([]chunk.ChunkView{legacy, missing, observed}, "1")
	if got.OTP != "75" || got.CancelPct != "10.0" || got.Cancel != "2" || got.Trips != "10" {
		t.Fatalf("summary = %+v", got)
	}
}

func TestParseRouteDay(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"?date=2026-06-12", "2026-06-12"},
		{"?from=2026-06-12", "2026-06-12"},
		{"?month=2026-06", "2026-06-01"},
		{"?date=2025-12-31", "2026-01-01"},
		{"?date=2999-01-01", ServiceDate().Format("2006-01-02")},
		{"?date=invalid", ServiceDate().Format("2006-01-02")},
		{"", ServiceDate().Format("2006-01-02")},
	} {
		req := httptest.NewRequest(http.MethodGet, "/transit/routes"+tc.query, nil)
		dr := parseRouteDay(req, "2026-01-01")
		if dr.From != tc.want || dr.To != tc.want {
			t.Errorf("%s: %+v", tc.query, dr)
		}
	}
}

func TestRouteSummaryPagesWithoutRawEvents(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `
  DROP TABLE transit.stop_delay, transit.cancellation;
  CREATE TABLE transit.route(route_id text PRIMARY KEY, short_name text, long_name text, color text, text_color text);
  CREATE TABLE transit.route_pattern(pattern_id text PRIMARY KEY, route_id text);
  CREATE TABLE transit.route_pattern_stop(pattern_id text, stop_id text, sequence int, is_timepoint bool);
  INSERT INTO transit.route VALUES('1','1','Mainline','','');
  INSERT INTO transit.route_band_chunk(route_id,date,band,trip_count,otp_count,otp_on_time)
  VALUES ('1','2026-06-15','morning',10,20,15),('retired','2026-06-15','morning',5,10,5),
         ('1','2026-06-16','morning',99,100,1),('1','2026-05-20','morning',10,20,10),('1','2026-04-01','morning',10,20,10);
 `)
	installRouteDaySummary(t, db)
	svc := &Service{db: db, reporter: &Reporter{repo: NewRepo(db)}, routeTimepointLoader: func(context.Context, string, time.Time) ([]TimepointSchedule, error) {
		t.Fatal("summary requested raw timetable")
		return nil, nil
	}}
	h := &Handler{svc: svc}
	var directory RoutesViewModel
	h.render.TransitRoutes = func(vm RoutesViewModel) RenderFunc {
		directory = vm
		return func(context.Context, io.Writer) error { return nil }
	}
	rr := httptest.NewRecorder()
	h.transitRoutesPage(rr, httptest.NewRequest(http.MethodGet, "/transit/routes?date=2026-06-15", nil))
	if rr.Code != 200 || len(directory.RouteMeta) != 2 || len(directory.Summaries) != 2 {
		t.Fatalf("directory status=%d vm=%+v", rr.Code, directory)
	}
	if directory.Totals.Trips != "15" || directory.Totals.Cancelled != "0" {
		t.Fatalf("wrong day totals: %+v", directory.Totals)
	}
	routeQueryExec(t, db, `DROP TABLE transit.route_band_chunk`)
	rr = httptest.NewRecorder()
	h.transitRoutesPage(rr, httptest.NewRequest(http.MethodGet, "/transit/routes?date=2026-06-15", nil))
	if rr.Code != 200 || len(directory.Summaries) != 2 {
		t.Fatalf("daily directory depends on band detail: %d %+v", rr.Code, directory)
	}
	routeQueryExec(t, db, `DROP TABLE transit.route_day_summary`)

	rr = httptest.NewRecorder()
	h.transitRoutesPage(rr, httptest.NewRequest(http.MethodGet, "/transit/routes?date=2026-06-15", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("database error status=%d", rr.Code)
	}
}

func TestRemovedRoutePageRedirectsWithoutReads(t *testing.T) {
	h := &Handler{}
	for _, query := range []string{"", "?date=2026-06-15"} {
		rr := httptest.NewRecorder()
		h.routePage(rr, routeRequest(http.MethodGet, "/transit/route/1"+query, "1"))
		if rr.Code != http.StatusMovedPermanently || rr.Header().Get("Location") != "/transit/routes"+query {
			t.Fatalf("redirect: %d %s", rr.Code, rr.Header().Get("Location"))
		}
	}
}
