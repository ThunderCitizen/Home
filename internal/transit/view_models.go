package transit

import (
	"thundercitizen/internal/transit/chunk"
)

// StopAlert is alert info for a specific stop, passed to the map JS.
type StopAlert struct {
	Header      string `json:"header"`
	Description string `json:"description"`
}

// LiveViewModel contains data for the live map page (/transit).
type LiveViewModel struct {
	Alerts          []ActiveAlert              // route-level alerts (shown at top)
	CancelledRoutes []string                   // route IDs with active cancellations
	CancelledTrips  map[string][]CancelledTrip // route ID → cancelled trip details
	CancelIncidents []CancelIncident           // consecutive cancellations grouped
	StopAlerts      map[string][]StopAlert     // stop ID → alerts (for map markers)
	FleetSize       int                        // total unique vehicles ever seen
	BusCount        int                        // route-assigned vehicles live now (server-rendered badge)
	ClockTime       string                     // HH:MM in TZ at render — initial value, JS ticks it
	ClockDate       string                     // "Month D" in TZ at render — initial value, JS refreshes it
	NoServiceRoutes []string                   // route IDs with no service today
	RouteMeta       []RouteMetaAPI             // colors, names, terminals for JS
}

// DateRange is the user-selectable window for metrics/routes pages.
// Default = trailing 7 days from today. Driven by ?from=&to= query params,
// rendered as two <input type="date"> fields in the header.
type DateRange struct {
	From    string // YYYY-MM-DD
	To      string // YYYY-MM-DD
	MinDate string // earliest selectable date (data-availability boundary)
	MaxDate string // latest selectable date (today)
}

// MetricsViewModel contains data for the metrics page (/transit/metrics).
//
// Chunks is the single source of truth for metrics — server templates and
// the embedded JS module both read it. The page embeds it via
// @templ.JSONScript so the frontend has the data on first paint without a
// fetch. CancelledTrips is a separate per-trip log embedded the same way
// for the cancel-card drill-down.
type MetricsViewModel struct {
	KPI            string         // active KPI key (otp, cancel, notice, wait, ewt, cv)
	RouteMeta      []RouteMetaAPI // needed for route comparison chart
	Range          DateRange
	Chunks         []chunk.ChunkView // 7 days × 3 bands × N routes — THE metrics shape
	CancelledTrips []CancelDetail    // per-trip cancel log for the date range
	HasData        bool
	ExportSize     string // approximate download size for Range, e.g. "~1.5 MB"
}

// RoutesViewModel contains data for the routes directory page (/transit/routes).
type RoutesViewModel struct {
	RouteMeta []RouteMetaAPI
	Range     DateRange
	Chunks    []chunk.ChunkView // same shape as MetricsViewModel.Chunks
}

// MethodViewModel holds data collection intervals for the methodology page.
type MethodViewModel struct {
	VehiclePoll string
	TripPoll    string
	AlertPoll   string
}

// TerminalCard is one selectable terminal on the terminals selector page.
type TerminalCard struct {
	StopID   string
	StopName string
}

// TerminalsViewModel contains data for the all-in-one terminals page
// (/transit/terminals). Renders the four canonical Thunder Bay
// terminals as tabs at the top of the page; the active tab's
// departures fill the rest of the viewport. RouteMeta is shipped so
// route pill colors are correct on first paint.
type TerminalsViewModel struct {
	Terminals []TerminalCard
	RouteMeta []RouteMetaAPI
}

// NewLiveViewModel creates the view model for the live map page.
func NewLiveViewModel(alerts []ActiveAlert, cancelledTrips map[string][]CancelledTrip) LiveViewModel {
	// Build cancelled routes list from trip details + alert-affected routes
	routeSet := make(map[string]bool)
	for r := range cancelledTrips {
		routeSet[r] = true
	}
	for _, a := range alerts {
		for _, r := range a.AffectedRoutes {
			routeSet[r] = true
		}
	}
	merged := make([]string, 0, len(routeSet))
	for r := range routeSet {
		merged = append(merged, r)
	}

	// Build stop-level alert map for map markers. Route-level alerts
	// without specific stops are surfaced in the top-of-page banner.
	//
	// Some upstream publishers model agency-wide notices (e.g. "today is a
	// holiday, Sunday schedule applies") by attaching the alert to every
	// stop_id as an informed_entity. Stamping 700+ map markers with the
	// same notice is noise — treat any alert that names this many stops as
	// system-wide and route it to the banner instead.
	const systemWideStopThreshold = 50
	stopAlerts := make(map[string][]StopAlert)
	var routeAlerts []ActiveAlert
	for _, a := range alerts {
		sa := StopAlert{}
		if a.Header != nil {
			sa.Header = *a.Header
		}
		if a.Description != nil {
			sa.Description = *a.Description
		}
		systemWide := len(a.AffectedStops) >= systemWideStopThreshold
		// Skip alerts with no human-readable content — they'd render as a
		// bare ⚠ on the marker with nothing to read.
		hasContent := sa.Header != "" || sa.Description != ""
		if len(a.AffectedStops) > 0 && !systemWide && hasContent {
			seen := map[string]bool{}
			for _, stopID := range a.AffectedStops {
				if !seen[stopID] {
					seen[stopID] = true
					stopAlerts[stopID] = append(stopAlerts[stopID], sa)
				}
			}
		}
		if systemWide || (len(a.AffectedRoutes) > 0 && len(a.AffectedStops) == 0) {
			routeAlerts = append(routeAlerts, a)
		}
	}

	return LiveViewModel{
		Alerts:          routeAlerts,
		CancelledRoutes: merged,
		CancelledTrips:  cancelledTrips,
		StopAlerts:      stopAlerts,
	}
}

// RouteViewModel contains data for the per-route detail page.
type RouteViewModel struct {
	RouteID            string
	ShortName          string
	LongName           string
	Color              string
	TextColor          string
	Date               string
	DateISO            string // YYYY-MM-DD for day picker links
	IsToday            bool   // true when viewing today's schedule (show actuals)
	Trips              []ScheduledTrip
	Alerts             []ActiveAlert
	TimepointSchedules []TimepointSchedule
	Unified            *UnifiedSchedule
	ServiceDays        map[string]bool // ISO dates with service this week
	CancelDays         map[string]int  // ISO date → cancellation count
	CancelledTrips     []CancelledTrip // cancelled trips for this route today
	TotalTrips         int             // total trip observations since tracking began
	TrackingSince      string          // first observation date (e.g. "Mar 20, 2026")
}
