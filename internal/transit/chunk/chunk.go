// Package chunk defines additive transit metric summaries and their shared reducers.
package chunk

import "time"

// Chunk is one route × service date × time band.
type Chunk struct {
	Quality
	RouteID     string
	Date        time.Time
	Band        string // "morning"|"midday"|"evening"
	ServiceKind string // "weekday"|"saturday"|"sunday"

	TripCount      int // distinct trips with any reported delay, by first departure band
	ScheduledCount int // exact archived service-date timetable
	CancelledCount int // distinct trips reported cancelled
	NoNoticeCount  int // cancellations first reported less than 15 minutes before departure

	BuiltAt time.Time
}

// Site classification thresholds, not a universal agency standard.
const (
	OTPEarlyLimit = -60.0 // 1 minute early (seconds)
	OTPLateLimit  = 300.0 // 5 minutes late (seconds)
)

// ChunkView is the JSON wire shape. Reduce raw counts and integrals with KPI;
// the precomputed display values are for a single row only.
type ChunkView struct {
	Quality
	// Identity
	RouteID     string `json:"route_id"`
	Date        string `json:"date"` // YYYY-MM-DD
	Band        string `json:"band"` // "morning" | "midday" | "evening"
	ServiceKind string `json:"service_kind"`

	Trips     int `json:"trips"`
	Scheduled int `json:"scheduled"`
	Cancelled int `json:"cancelled"`
	NoNotice  int `json:"no_notice"`

	// Pre-computed display values for direct rendering of a single chunk.
	// Do NOT use these when summing multiple chunks together — sum the
	// raw fields above and re-apply the formula instead.
	OTPPct *float64 `json:"otp_pct"` // null when unavailable
	EWTMin *float64 `json:"ewt_min"` // minutes
	Cv     *float64 `json:"cv"`      // dimensionless
}

// View uses the same reducer as multi-row cards and charts.
func (c Chunk) View() ChunkView {
	v := ChunkView{
		Quality:     c.Quality,
		ServiceKind: c.ServiceKind,
		RouteID:     c.RouteID,
		Date:        c.Date.Format("2006-01-02"),
		Band:        c.Band,
		Trips:       c.TripCount,
		Scheduled:   c.ScheduledCount,
		Cancelled:   c.CancelledCount,
		NoNotice:    c.NoNoticeCount,
	}
	reading := func(metric Metric) *float64 {
		value, ok := KPI([]ChunkView{v}, metric, "")
		if !ok {
			return nil
		}
		return &value
	}
	v.OTPPct = reading(MetricOTP)
	v.EWTMin = reading(MetricEWT)
	v.Cv = reading(MetricCv)
	return v
}

// Version changes whenever a recipe's meaning changes. Legacy rollups remain
// stored for audit, but cannot silently mix with corrected trend readings.
const Version = 1

// Quality contains additive sample counts and integrals. CV is calculated
// within one stop/direction/day/band before it is weighted; pooling headways
// across timetable changes would manufacture irregularity.
type Quality struct {
	Version            int     `json:"metric_version"`
	OTPCount           int     `json:"otp_count"`
	OTPOnTime          int     `json:"otp_on_time"`
	Early              int     `json:"early_count"`
	Late               int     `json:"late_count"`
	ExpectedTimepoints int     `json:"expected_timepoints"`
	ObservedTimepoints int     `json:"observed_timepoints"`
	EligibleWindows    int     `json:"eligible_windows"`
	TotalWindows       int     `json:"total_windows"`
	WaitObservedArea   float64 `json:"wait_observed_area"`
	WaitScheduledArea  float64 `json:"wait_scheduled_area"`
	WindowSeconds      float64 `json:"window_seconds"`
	CVWeightedSum      float64 `json:"cv_weighted_sum"`
	CVWeight           float64 `json:"cv_weight"`
}
