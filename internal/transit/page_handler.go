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
	live, err := h.svc.Live()
	if err != nil {
		middleware.HandleUnavailable(r.Context(), w, "live data unavailable", err)
		return
	}
	if live == nil || live.dashboard == nil {
		httperr.Unavailable(w, "live data cache warming")
		return
	}

	vm := NewLiveViewModel(live.dashboard.Alerts, live.dashboard.CancelledTrips)
	vm.FleetSize = live.dashboard.FleetSize
	vm.CancelIncidents = live.incidents
	vm.NoServiceRoutes = live.noService
	vm.RouteMeta = h.svc.RouteMeta()

	h.render.TransitLive(vm)(r.Context(), w)
}

func (h *Handler) transitMetricsPage(w http.ResponseWriter, r *http.Request) {
	var vm MetricsViewModel
	vm.KPI = "otp"
	vm.RouteMeta = h.svc.RouteMeta()
	vm.Range = parseDateRange(r, h.svc.SinceDate(r.Context()))

	from, errFrom := time.ParseInLocation("2006-01-02", vm.Range.From, TZ)
	to, errTo := time.ParseInLocation("2006-01-02", vm.Range.To, TZ)
	if errFrom == nil && errTo == nil {
		// Chunks and cancel details are independent reads. Run them in
		// parallel so wall time collapses to whichever is slower instead
		// of the sum. Both fail open (page renders empty cells).
		g, ctx := errgroup.WithContext(r.Context())
		g.Go(func() error {
			if chunks, err := h.svc.Chunks(ctx, from, to); err == nil {
				vm.Chunks = chunks
				vm.HasData = len(chunks) > 0
			}
			return nil
		})
		g.Go(func() error {
			if cancels, err := LoadCancelDetails(ctx, h.svc.db, from, to); err == nil {
				vm.CancelledTrips = cancels
			}
			return nil
		})
		_ = g.Wait()
	}

	h.render.TransitMetrics(vm)(r.Context(), w)
}

func (h *Handler) transitRoutesPage(w http.ResponseWriter, r *http.Request) {
	var vm RoutesViewModel
	vm.RouteMeta = h.svc.RouteMeta()
	vm.Range = parseDateRange(r, h.svc.SinceDate(r.Context()))
	if from, err := time.ParseInLocation("2006-01-02", vm.Range.From, TZ); err == nil {
		if to, err := time.ParseInLocation("2006-01-02", vm.Range.To, TZ); err == nil {
			if chunks, err := h.svc.Chunks(r.Context(), from, to); err == nil {
				vm.Chunks = chunks
			}
		}
	}
	h.render.TransitRoutes(vm)(r.Context(), w)
}

func (h *Handler) transitMethodPage(w http.ResponseWriter, r *http.Request) {
	h.render.TransitMethod(MethodViewModel{
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
		RouteMeta: h.svc.RouteMeta(),
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

	// Full page: dedicated route page with schedule + stats.
	if partial == "" {
		date := ServiceDate()
		if !schedDate.IsZero() {
			date = schedDate
		}

		// All six fetches below are independent. Run them concurrently so
		// wall-clock time collapses to the slowest single query instead of
		// the sum. RouteInfo is the only one whose error 404s; the rest
		// surface 500 or silently render empty.
		var (
			info          *RouteInfo
			tp            []TimepointSchedule
			totalTrips    int
			since         string
			serviceDays   map[string]bool
			cancelDays    map[string]int
			cancelledList []CancelledTrip
		)
		g, ctx := errgroup.WithContext(r.Context())
		g.Go(func() error {
			v, err := h.svc.RouteInfo(ctx, routeID)
			if err != nil {
				return err
			}
			info = v
			return nil
		})
		g.Go(func() error {
			v, err := h.svc.RouteTimepointSchedule(ctx, routeID, date)
			if err != nil {
				return err
			}
			tp = v
			return nil
		})
		g.Go(func() error {
			totalTrips, since = h.svc.RouteTrackingStats(ctx, routeID)
			return nil
		})
		g.Go(func() error {
			serviceDays = h.svc.RouteServiceDays(ctx, routeID, date)
			return nil
		})
		g.Go(func() error {
			cancelDays = h.svc.RouteCancelDays(ctx, routeID, date)
			return nil
		})
		g.Go(func() error {
			// Cancellation details is best-effort; errors here just leave the
			// list empty rather than 500ing the whole page (matching prior
			// behavior via `if ... == nil`).
			if v, err := CancelledTripDetailsForRoute(ctx, h.svc.db, date, routeID); err == nil {
				cancelledList = v
			}
			return nil
		})
		if err := g.Wait(); err != nil {
			// RouteInfo failure is a 404; anything else reported by errgroup
			// came from RouteTimepointSchedule (the only other returning error).
			if info == nil {
				http.NotFound(w, r)
				return
			}
			httperr.Internal(w, err)
			return
		}

		var sinceLabel string
		if since != "" {
			if t, err := time.ParseInLocation("2006-01-02", since, TZ); err == nil {
				sinceLabel = t.Format("Jan 2, 2006")
			}
		}
		vm := RouteViewModel{
			RouteID:        info.RouteID,
			ShortName:      info.ShortName,
			LongName:       info.LongName,
			Color:          info.Color,
			TextColor:      info.TextColor,
			Date:           date.Format("Monday, January 2"),
			DateISO:        date.Format("2006-01-02"),
			IsToday:        date.Format("2006-01-02") == ServiceDate().Format("2006-01-02"),
			Unified:        UnifySchedules(tp),
			TotalTrips:     totalTrips,
			TrackingSince:  sinceLabel,
			ServiceDays:    serviceDays,
			CancelDays:     cancelDays,
			CancelledTrips: cancelledList,
		}
		h.render.Route(vm)(r.Context(), w)
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
