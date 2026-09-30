package chunk

var Bands = []string{"morning", "midday", "evening"}

type Metric string

const (
	MetricOTP    Metric = "otp"
	MetricCancel Metric = "cancel"
	MetricNotice Metric = "notice"
	MetricWait   Metric = "wait"
	MetricEWT    Metric = "ewt"
	MetricCv     Metric = "cv"
)

// KPI reduces corrected chunks. Counts and wait integrals are additive. EWT
// and CV first combine matched windows within each route, then average routes
// equally. These are sampled service measures, not passenger-weighted results.
// Missing observations and legacy recipe versions never become zero readings.
func KPI(chunks []ChunkView, metric Metric, band string) (float64, bool) {
	type routeSum struct{ numerator, denominator float64 }
	perRoute := map[string]routeSum{}
	var numerator, denominator float64
	for _, c := range chunks {
		if c.Version != Version || (band != "" && c.Band != band) {
			continue
		}
		switch metric {
		case MetricOTP:
			numerator += float64(c.OTPOnTime) * 100
			denominator += float64(c.OTPCount)
		case MetricCancel:
			// No telemetry at all cannot establish a zero-cancellation day.
			if c.Trips == 0 && c.Cancelled == 0 {
				continue
			}
			numerator += float64(c.Cancelled) * 100
			denominator += float64(c.Scheduled)
		case MetricNotice:
			numerator += float64(c.NoNotice) * 100
			denominator += float64(c.Cancelled)
		case MetricWait:
			numerator += c.WaitObservedArea / 60
			denominator += c.WindowSeconds
		case MetricEWT, MetricCv:
			a := perRoute[c.RouteID]
			if metric == MetricEWT {
				a.numerator += (c.WaitObservedArea - c.WaitScheduledArea) / 60
				a.denominator += c.WindowSeconds
			} else {
				a.numerator += c.CVWeightedSum
				a.denominator += c.CVWeight
			}
			perRoute[c.RouteID] = a
		}
	}
	if metric == MetricEWT || metric == MetricCv {
		for _, a := range perRoute {
			if a.denominator > 0 {
				numerator += a.numerator / a.denominator
				denominator++
			}
		}
	}
	if denominator <= 0 {
		return 0, false
	}
	return numerator / denominator, true
}
