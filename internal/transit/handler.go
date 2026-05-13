package transit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Renderer renders page templates. Injected to avoid import cycles
// between the transit package and templates/pages.
type Renderer struct {
	TransitLive               func(vm LiveViewModel) RenderFunc
	TransitMetrics            func(vm MetricsViewModel) RenderFunc
	TransitRoutes             func(vm RoutesViewModel) RenderFunc
	TransitMethod             func(vm MethodViewModel) RenderFunc
	Route                     func(vm RouteViewModel) RenderFunc
	RoutePartial              func(vm RouteViewModel) RenderFunc
	RouteSchedulePartial      func(vm RouteViewModel) RenderFunc
	RouteScheduleTodayPartial func(vm RouteViewModel) RenderFunc
	RouteScheduleBodyPartial  func(vm RouteViewModel) RenderFunc
	AuditIndex                func(vm AuditIndexViewModel) RenderFunc
	AuditRoute                func(vm AuditRouteViewModel) RenderFunc
	Terminals                 func(vm TerminalsViewModel) RenderFunc
	PlanPartial               func(plan *PlanResult, summary bool, fromLat, fromLon, toLat, toLon float64) RenderFunc
}

// RenderFunc writes rendered HTML to the writer.
type RenderFunc func(ctx context.Context, w io.Writer) error

// Handler serves all transit page and API routes.
// It is a thin HTTP adapter — business logic and caching live in Service.
// The handler methods are split across three files: this one holds shared
// infrastructure (struct, constructor, route registration, parseDateRange,
// helpers); page handlers live in page_handler.go; API handlers live in
// api_handler.go.
type Handler struct {
	svc           *Service
	render        Renderer
	VehicleStream *VehicleStream
}

// NewHandler creates a transit handler backed by a Service. The recorder is
// threaded through so stop predictions can read the live trip feed from its
// in-memory snapshot.
func NewHandler(db *pgxpool.Pool, render Renderer, recorder *Recorder) *Handler {
	svc := NewService(db, recorder)
	return &Handler{
		svc:           svc,
		render:        render,
		VehicleStream: svc.stream,
	}
}

// StartLiveWarmer launches the background goroutine that keeps the live
// dashboard slot warm. See LiveWarmer for details.
func (h *Handler) StartLiveWarmer(ctx context.Context) {
	NewLiveWarmer(h.svc).Start(ctx)
}

// PageRoutes returns a chi.Router with transit page routes.
// Mount at /transit.
func (h *Handler) PageRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.transitLivePage)
	r.Get("/metrics", h.transitMetricsPage)
	r.Get("/routes", h.transitRoutesPage)
	r.Get("/method", h.transitMethodPage)
	r.Get("/report", h.transitReport)
	r.Get("/route/{id}", h.routePage)
	r.Get("/kiosk", h.transitTerminalsPage)
	r.Get("/terminals", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/transit/kiosk", http.StatusMovedPermanently)
	})
	r.Get("/audit/deltas", h.auditIndex)
	r.Get("/audit/deltas/{id}", h.auditRoute)
	return r
}

// APIRoutes returns a chi.Router with transit API routes.
// Mount at /api/transit.
func (h *Handler) APIRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/vehicles", h.vehicles)
	r.Get("/vehicles.json", h.vehiclesJSON)
	r.Get("/vehicles/stream", h.vehiclesSSE)
	r.Get("/stats", h.stats)
	r.Get("/stops/nearby", h.nearbyStops)
	r.Get("/stops", h.stops)
	r.Get("/stop/{id}/predictions", h.stopPredictions)
	r.Get("/plan", h.plan)
	r.Get("/vehicle/{vehicleID}/distance/{stopID}", h.vehicleDistance)
	r.Get("/stops/analytics", h.stopAnalytics)
	r.Get("/timepoints", h.timepoints)
	r.Get("/routes", h.routesMeta)
	return r
}

// MaxRangeDays caps how wide a custom window can be. One year keeps
// query cost bounded and the chunk cache from being asked to hydrate
// the universe on a malformed link.
const MaxRangeDays = 366

// parseDateRange builds a DateRange from ?from= and ?to= query params.
// Both omitted → trailing 7 days ending today. Either malformed → falls
// back to the default. To is clamped to today, From to today-365. If
// from > to they're swapped. If the span exceeds MaxRangeDays, From
// gets pulled forward to To - 365.
func parseDateRange(r *http.Request, sinceDate string) DateRange {
	today := Today()
	q := r.URL.Query()

	parse := func(s string) (time.Time, bool) {
		t, err := time.ParseInLocation("2006-01-02", s, TZ)
		return t, err == nil
	}

	to := today
	if t, ok := parse(q.Get("to")); ok {
		to = t
	}
	if to.After(today) {
		to = today
	}

	from := to.AddDate(0, 0, -6)
	if t, ok := parse(q.Get("from")); ok {
		from = t
	}

	if from.After(to) {
		from, to = to, from
	}

	if span := int(to.Sub(from).Hours()/24) + 1; span > MaxRangeDays {
		from = to.AddDate(0, 0, -(MaxRangeDays - 1))
	}

	min := from
	if sinceDate != "" {
		if parsed, ok := parse(sinceDate); ok {
			min = parsed
		}
	}

	return DateRange{
		From:    from.Format("2006-01-02"),
		To:      to.Format("2006-01-02"),
		MinDate: min.Format("2006-01-02"),
		MaxDate: today.Format("2006-01-02"),
	}
}

// --- helpers ---

func fmtDelay(sec float64) string {
	abs := sec
	if abs < 0 {
		abs = -abs
	}
	sign := ""
	if sec < 0 {
		sign = "-"
	}
	if abs < 60 {
		return fmt.Sprintf("%s%.0fs", sign, abs)
	}
	m := int(abs) / 60
	s := int(abs) % 60
	if s > 0 {
		return fmt.Sprintf("%s%dm%ds", sign, m, s)
	}
	return fmt.Sprintf("%s%dm", sign, m)
}

func writeJSON(w http.ResponseWriter, cacheControl string, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", cacheControl)
	json.NewEncoder(w).Encode(v)
}

func parseDays(r *http.Request, defaultDays, maxDays int) int {
	days := defaultDays
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= maxDays {
			days = parsed
		}
	}
	return days
}
