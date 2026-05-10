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
	r.Get("/terminals", h.transitTerminalsPage)
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

// parseDateRange builds a DateRange from the "end" query param.
// No param = 7-day trailing from today. With param = 7 days ending on that date.
//
// The window is ALWAYS 7 days wide. If the underlying data only goes back
// 2 days, we still render a 7-day grid — the empty days show up as
// blank cells, which is more honest than collapsing the window. The
// PrevURL link below still gets disabled at the data boundary so users
// can't keep clicking into the void, but the visible shape stays
// consistent at 7 × 3 cells regardless of how much history we have.
func parseDateRange(r *http.Request, sinceDate string) DateRange {
	today := Today()

	// Default: trailing 7 days ending today
	to := today

	if s := r.URL.Query().Get("end"); s != "" {
		if parsed, err := time.ParseInLocation("2006-01-02", s, TZ); err == nil {
			to = parsed
			if to.After(today) {
				to = today
			}
		}
	}

	from := to.AddDate(0, 0, -6)

	// Parse the data-availability boundary for the prev/next arrow
	// disable logic, but DO NOT clamp `from` to it — the window stays
	// 7 days wide even when we don't have data that far back.
	var since time.Time
	if sinceDate != "" {
		if parsed, err := time.ParseInLocation("2006-01-02", sinceDate, TZ); err == nil {
			since = parsed
		}
	}

	// Format label: "Mon Mar 24 – Sun Mar 30"
	days := [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	mos := [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	fmtD := func(t time.Time) string {
		return fmt.Sprintf("%s %s %d", days[t.Weekday()], mos[t.Month()-1], t.Day())
	}

	basePath := r.URL.Path

	dr := DateRange{
		From:  from.Format("2006-01-02"),
		To:    to.Format("2006-01-02"),
		Label: fmtD(from) + " – " + fmtD(to),
	}

	dr.IsLatest = !to.Before(today)

	// Prev: 7 days earlier
	prevTo := to.AddDate(0, 0, -7)
	if !since.IsZero() && prevTo.Before(since) {
		dr.PrevURL = ""
	} else {
		dr.PrevURL = basePath + "?end=" + prevTo.Format("2006-01-02")
	}

	// Next: 7 days later (no param if it lands on today)
	atEnd := !to.Before(today)
	if !atEnd {
		nextTo := to.AddDate(0, 0, 7)
		if !nextTo.Before(today) {
			dr.NextURL = basePath // no param = latest
		} else {
			dr.NextURL = basePath + "?end=" + nextTo.Format("2006-01-02")
		}
	}

	return dr
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
