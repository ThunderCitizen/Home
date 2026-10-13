package transit

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"

	"thundercitizen/internal/httperr"
	"thundercitizen/internal/middleware"
)

// formatPollInterval renders a poll cadence like time.Duration.String() but
// drops trailing zero units ("1m0s" → "1m") for cleaner copy in the UI.
func formatPollInterval(d time.Duration) string {
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	s = strings.TrimSuffix(s, "0m")
	if s == "" {
		return "0s"
	}
	return s
}

// --- Page handlers ---

func (h *Handler) transitLivePage(w http.ResponseWriter, r *http.Request) {
	live, err := h.svc.Live(r.Context())
	if err != nil {
		middleware.HandleUnavailable(r.Context(), w, "live data unavailable", err)
		return
	}
	vm := NewLiveViewModel(live.dashboard.Alerts, live.dashboard.CancelledTrips)
	vm.FleetSize = live.dashboard.FleetSize
	vm.BusCount = h.svc.LiveBusCount()
	now := Now()
	vm.ClockTime = now.Format("15:04")
	vm.ClockDate = now.Format("January 2")
	vm.CancelIncidents = live.incidents
	vm.NoServiceRoutes = live.noService
	vm.RouteMeta = h.svc.RouteMeta(r.Context())

	h.render.TransitLive(vm)(r.Context(), w)
}

func (h *Handler) transitMetricsPage(w http.ResponseWriter, r *http.Request) {
	var vm MetricsViewModel
	vm.KPI = "otp"
	vm.RouteMeta = h.svc.RouteMeta(r.Context())
	since := h.svc.SinceDate(r.Context())
	vm.Month, vm.Range = parseMetricsMonth(r, since)

	from, errFrom := time.ParseInLocation("2006-01-02", vm.Range.From, TZ)
	to, errTo := time.ParseInLocation("2006-01-02", vm.Range.To, TZ)
	if errFrom == nil && errTo == nil {
		// Read history once and derive the selected month's cards from it.
		// A failed query is an unavailable page, not an empty success response.
		trendEnd := to
		if completed := ServiceDate().AddDate(0, 0, -1); trendEnd.After(completed) {
			trendEnd = completed
		}
		vm.TrendChunks, errFrom = h.svc.Chunks(r.Context(), mustParseDate(since, from), trendEnd)
		if errFrom != nil {
			middleware.HandleUnavailable(r.Context(), w, "metric history unavailable", errFrom)
			return
		}

		for _, c := range vm.TrendChunks {
			if c.Date >= vm.Range.From && c.Date <= vm.Range.To {
				vm.Chunks = append(vm.Chunks, c)
			}
		}
		vm.HasData = len(vm.Chunks) > 0
	}

	h.render.TransitMetrics(vm)(r.Context(), w)
}

func mustParseDate(value string, fallback time.Time) time.Time {
	if parsed, err := time.ParseInLocation("2006-01-02", value, TZ); err == nil {
		return parsed
	}
	return fallback
}

func (h *Handler) transitRoutesPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "HX-Request, HX-History-Restore-Request")
	var vm RoutesViewModel
	since, err := h.svc.RouteSummarySinceDate(r.Context())
	if err != nil {
		middleware.HandleUnavailable(r.Context(), w, "route summaries unavailable", err)
		return
	}
	vm.Range = parseRouteDay(r, since)
	from := mustParseDate(vm.Range.From, ServiceDate())
	to := mustParseDate(vm.Range.To, ServiceDate())
	g, ctx := errgroup.WithContext(r.Context())
	g.Go(func() error {
		var err error
		vm.RouteMeta, err = h.svc.reporter.repo.SummaryRouteMeta(ctx, from, to)
		return err
	})
	g.Go(func() error {
		var err error
		vm.Summaries, err = h.svc.RouteDaySummaries(ctx, from)
		return err
	})
	if err := g.Wait(); err != nil {
		middleware.HandleUnavailable(r.Context(), w, "route summaries unavailable", err)
		return
	}

	vm.Totals = TotalRouteDayStats(vm.Summaries)
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		h.render.TransitRoutesDay(vm)(r.Context(), w)
		return
	}
	h.render.TransitRoutes(vm)(r.Context(), w)
}

func (h *Handler) transitMethodPage(w http.ResponseWriter, r *http.Request) {
	base, err := h.svc.PublishedReportDownloadURL(r.Context())
	if err != nil {
		middleware.HandleUnavailable(r.Context(), w, "published reports unavailable", err)
		return
	}
	h.render.TransitMethod(MethodViewModel{
		ReportURL:   base,
		VehiclePoll: formatPollInterval(VehiclePollInterval),
		TripPoll:    formatPollInterval(TripPollInterval),
		AlertPoll:   formatPollInterval(AlertPollInterval),
	})(r.Context(), w)
}

func (h *Handler) transitReport(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/transit", http.StatusMovedPermanently)
}

// canonicalTerminals is the curated list of Thunder Bay Transit
// terminals shown on the kiosk picker. Hardcoded by stop_id so the
// list stays stable regardless of GTFS metadata drift (the upstream
// feed does not flag any stop as is_terminal=true). If a terminal
// stop_id ever changes upstream, that's a data event worth a code
// change anyway — better to fail visibly here than silently drop a
// terminal from the picker.
var canonicalTerminals = []TerminalCard{
	{StopID: "1121", StopName: "Waterfront Terminal"},
	{StopID: "1019", StopName: "City Hall Terminal"},
	{StopID: "1231", StopName: "Confederation College"},
	{StopID: "1222", StopName: "Lakehead University"},
}

// transitTerminalsPage renders the all-in-one terminals departures
// page: four tabs at the top, the active terminal's board fills the
// rest of the viewport. The client polls
// /api/transit/stop/{id}/predictions for whichever tab is active and
// re-polls immediately on tab switch. RouteMeta ships with the page so
// route colors are correct on first paint.
func (h *Handler) transitTerminalsPage(w http.ResponseWriter, r *http.Request) {
	vm := TerminalsViewModel{
		Terminals: canonicalTerminals,
		RouteMeta: h.svc.RouteMeta(r.Context()),
	}
	h.render.Terminals(vm)(r.Context(), w)
}

func (h *Handler) routePage(w http.ResponseWriter, r *http.Request) {
	routeID := chi.URLParam(r, "id")
	if routeID == "" {
		http.NotFound(w, r)
		return
	}

	// Optional date parameter for schedule views.
	var schedDate time.Time
	if ds := r.URL.Query().Get("date"); ds != "" {
		if parsed, err := time.ParseInLocation("2006-01-02", ds, TZ); err == nil {
			schedDate = parsed
		}
	}

	partial := r.URL.Query().Get("partial")

	// The directory replaces standalone route metrics. Keep live timetable
	// partials available and redirect old bookmarks without loading any data.
	if partial == "" {
		target := "/transit/routes"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	date := ServiceDate()
	if partial != "schedule-today" && !schedDate.IsZero() {
		date = schedDate
	}
	if h.archivedHistory(w, r, date) {
		return
	}

	// Fast path: schedule-body only needs schedule + timetable, skip metrics
	if partial == "schedule-body" {
		date := ServiceDate()
		if !schedDate.IsZero() {
			date = schedDate
		}
		info, err := h.svc.RouteInfo(r.Context(), routeID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		tp, err := h.svc.RouteTimepointSchedule(r.Context(), routeID, date)
		if err != nil {
			httperr.Internal(w, err)
			return
		}
		vm := RouteViewModel{
			RouteID: info.RouteID,
			IsToday: date.Format("2006-01-02") == ServiceDate().Format("2006-01-02"),
			Unified: UnifySchedules(tp),
		}
		h.render.RouteScheduleBodyPartial(vm)(r.Context(), w)
		return
	}

	// Fast path for schedule partials — only need schedule + timetable, skip metrics.
	if partial == "1" || partial == "schedule" || partial == "schedule-today" {
		date := ServiceDate()
		if partial != "schedule-today" && !schedDate.IsZero() {
			date = schedDate
		}
		info, err := h.svc.RouteInfo(r.Context(), routeID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		tp, err := h.svc.RouteTimepointSchedule(r.Context(), routeID, date)
		if err != nil {
			httperr.Internal(w, err)
			return
		}
		vm := RouteViewModel{
			RouteID:   info.RouteID,
			ShortName: info.ShortName,
			LongName:  info.LongName,
			Color:     info.Color,
			TextColor: info.TextColor,
			DateISO:   date.Format("2006-01-02"),
			IsToday:   date.Format("2006-01-02") == ServiceDate().Format("2006-01-02"),
			Unified:   UnifySchedules(tp),
		}
		if partial == "schedule-today" {
			h.render.RouteScheduleTodayPartial(vm)(r.Context(), w)
		} else {
			vm.ServiceDays = h.svc.RouteServiceDays(r.Context(), routeID, date)
			vm.CancelDays = h.svc.RouteCancelDays(r.Context(), routeID, date)
			if partial == "1" {
				if cancels, err := CancelledTripDetailsForRoute(r.Context(), h.svc.db, date, routeID); err == nil {
					vm.CancelledTrips = cancels
				}
			}
			if partial == "schedule" {
				h.render.RouteSchedulePartial(vm)(r.Context(), w)
			} else {
				h.render.RoutePartial(vm)(r.Context(), w)
			}
		}
		return
	}

	http.NotFound(w, r)
}
