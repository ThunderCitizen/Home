package transit

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"thundercitizen/internal/cache"
	"thundercitizen/internal/httperr"
)

// --- API handlers ---

func (h *Handler) vehicles(w http.ResponseWriter, r *http.Request) {
	raw := h.VehicleStream.RawFeed()
	if raw == nil {
		httperr.Unavailable(w, "vehicle feed not available")
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Cache-Control", cache.Live)
	w.Write(raw)
}

func (h *Handler) vehiclesSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Disable the server-wide 15s WriteTimeout for this long-lived stream.
	// Without this, every SSE connection would be force-closed ~15s after
	// it opens, producing NS_ERROR_NET_* in Firefox and forcing the browser
	// to reconnect every keepalive tick. Scoped per-connection — other
	// routes retain the global 15s WriteTimeout protection.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		streamLog.Warn("sse: could not clear write deadline", "err", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", cache.Live)
	w.Header().Set("Connection", "keep-alive")

	// Send current state immediately
	if cur := h.VehicleStream.Current(); cur != nil {
		fmt.Fprintf(w, "data: %s\n\n", cur)
		flusher.Flush()
	}

	// Subscribe to updates
	ch := h.VehicleStream.Subscribe()
	defer h.VehicleStream.Unsubscribe(ch)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case payload, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) vehiclesJSON(w http.ResponseWriter, r *http.Request) {
	cur := h.VehicleStream.Current()
	if cur == nil {
		httperr.Unavailable(w, "vehicle feed not available")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", cache.Live)
	w.Write(cur)
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	variant := r.URL.Query().Get("range")
	if variant == "" {
		variant = "day"
	}

	report := h.svc.Stats(variant)
	if report == nil {
		httperr.Unavailable(w, "stats unavailable")
		return
	}
	writeJSON(w, cache.Short, report)
}

func (h *Handler) stops(w http.ResponseWriter, r *http.Request) {
	allStops := h.svc.AllStops()
	if allStops == nil {
		httperr.Unavailable(w, "stops unavailable")
		return
	}
	// Stop inventory changes only when GTFS reloads.
	writeJSON(w, cache.Reference, allStops)
}

func (h *Handler) stopAnalytics(w http.ResponseWriter, r *http.Request) {
	results := h.svc.StopAnalytics()
	if results == nil {
		httperr.Unavailable(w, "stop analytics unavailable")
		return
	}
	writeJSON(w, cache.Page, results)
}

func (h *Handler) routesMeta(w http.ResponseWriter, r *http.Request) {
	routes := h.svc.RouteMeta()
	if routes == nil {
		httperr.Unavailable(w, "route meta unavailable")
		return
	}
	// Route metadata only changes when GTFS reloads (rare), so tell the
	// browser it can cache this for an hour without revalidating.
	writeJSON(w, cache.Reference, routes)
}

func (h *Handler) timepoints(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.TimepointStops(r.Context())
	if err != nil {
		httperr.Internal(w, err)
		return
	}
	writeJSON(w, cache.Reference, result)
}

func (h *Handler) stopPredictions(w http.ResponseWriter, r *http.Request) {
	stopID := chi.URLParam(r, "id")
	stopID = strings.TrimSuffix(stopID, "/predictions")
	stopID = strings.TrimRight(stopID, "/")
	if stopID == "" {
		httperr.BadRequest(w, "missing stop_id")
		return
	}

	predictions, err := h.svc.StopPredictions(r.Context(), stopID)
	if err != nil {
		httperr.Internal(w, err)
		return
	}
	writeJSON(w, cache.Short, predictions)
}

// --- trip planner ---

func (h *Handler) plan(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	fromLat, err := strconv.ParseFloat(q.Get("from_lat"), 64)
	if err != nil {
		httperr.BadRequest(w, "invalid from_lat")
		return
	}
	fromLon, err := strconv.ParseFloat(q.Get("from_lon"), 64)
	if err != nil {
		httperr.BadRequest(w, "invalid from_lon")
		return
	}
	toLat, err := strconv.ParseFloat(q.Get("to_lat"), 64)
	if err != nil {
		httperr.BadRequest(w, "invalid to_lat")
		return
	}
	toLon, err := strconv.ParseFloat(q.Get("to_lon"), 64)
	if err != nil {
		httperr.BadRequest(w, "invalid to_lon")
		return
	}

	now := Now()

	// Parse date, default to today
	date := now
	if d := q.Get("date"); d != "" {
		if parsed, err := time.Parse("2006-01-02", d); err == nil {
			date = parsed
		}
	}

	var result *PlanResult

	if arriveBy := q.Get("arrive_by"); arriveBy != "" {
		// Arrive-by mode: find latest departure that arrives on time
		var ah, am int
		if _, err := fmt.Sscanf(arriveBy, "%d:%d", &ah, &am); err != nil {
			httperr.BadRequest(w, "invalid arrive_by (use HH:MM)")
			return
		}
		result, err = h.svc.TripPlanArriveBy(r.Context(),
			LatLng{fromLat, fromLon}, LatLng{toLat, toLon},
			q.Get("from_stop"), q.Get("to_stop"),
			ah*3600+am*60, date)
	} else {
		// Depart-at mode (default)
		departSec := now.Hour()*3600 + now.Minute()*60
		if d := q.Get("depart"); d != "" {
			var dh, dm int
			if _, err := fmt.Sscanf(d, "%d:%d", &dh, &dm); err == nil {
				departSec = dh*3600 + dm*60
			}
		}
		result, err = h.svc.TripPlan(r.Context(),
			LatLng{fromLat, fromLon}, LatLng{toLat, toLon},
			q.Get("from_stop"), q.Get("to_stop"),
			departSec, date)
	}
	if err != nil {
		httperr.Internal(w, err)
		return
	}

	if q.Get("partial") != "" {
		summary := q.Get("summary") == "1"
		h.render.PlanPartial(result, summary, fromLat, fromLon, toLat, toLon)(r.Context(), w)
		return
	}
	writeJSON(w, cache.Live, result)
}

// --- spatial handlers ---

func (h *Handler) nearbyStops(w http.ResponseWriter, r *http.Request) {
	lat, err := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	if err != nil {
		httperr.BadRequest(w, "invalid lat parameter")
		return
	}
	lon, err := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	if err != nil {
		httperr.BadRequest(w, "invalid lon parameter")
		return
	}

	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	stops, err := h.svc.NearbyStops(r.Context(), lat, lon, limit)
	if err != nil {
		httperr.Internal(w, err)
		return
	}
	writeJSON(w, cache.Reference, stops)
}

func (h *Handler) vehicleDistance(w http.ResponseWriter, r *http.Request) {
	vehicleID := chi.URLParam(r, "vehicleID")
	stopID := chi.URLParam(r, "stopID")
	if vehicleID == "" || stopID == "" {
		httperr.BadRequest(w, "missing vehicleID or stopID")
		return
	}

	dist, err := h.svc.VehicleDistance(r.Context(), vehicleID, stopID)
	if errors.Is(err, pgx.ErrNoRows) {
		httperr.NotFound(w, "vehicle or stop not found")
		return
	}
	if err != nil {
		httperr.Internal(w, err)
		return
	}
	writeJSON(w, cache.Live, dist)
}
