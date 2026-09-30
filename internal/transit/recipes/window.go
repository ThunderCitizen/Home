package recipes

import (
	"math"
	"sort"

	"thundercitizen/internal/transit/chunk"
)

// WindowStats compares random-arrival wait on the same bounded interval for
// both schedules. Times are seconds from service midnight. The caller must
// first establish that every scheduled passage is observed or explicitly
// cancelled. Unknown telemetry cannot be interpreted as a service gap.
//
// Integrating nextArrival-t handles irregular schedules and band edges without
// assuming scheduled wait equals half the mean headway. Negative EWT is valid.
func WindowStats(scheduled, observed []float64, start, end float64) chunk.Quality {
	var q chunk.Quality
	if len(scheduled) < 3 || len(observed) < 3 {
		return q
	}
	scheduled = append([]float64(nil), scheduled...)
	observed = append([]float64(nil), observed...)
	sort.Float64s(scheduled)
	sort.Float64s(observed)
	start = math.Max(start, math.Max(scheduled[0], observed[0]))
	end = math.Min(end, math.Min(scheduled[len(scheduled)-1], observed[len(observed)-1]))
	if end <= start {
		return q
	}
	q.WindowSeconds = end - start
	q.WaitObservedArea = waitArea(observed, start, end)
	q.WaitScheduledArea = waitArea(scheduled, start, end)
	var n int
	var sum, sumSq float64
	for i := 1; i < len(observed); i++ {
		// Complete headways ending in the window; never truncate a gap to
		// manufacture a short headway at the band boundary.
		if observed[i] <= start || observed[i] > end {
			continue
		}
		h := observed[i] - observed[i-1]
		if h < 0 {
			continue
		}
		n++
		sum += h
		sumSq += h * h
	}
	if n >= 2 && sum > 0 {
		q.CVWeight = q.WindowSeconds
		q.CVWeightedSum = chunk.Cv(n, sum, sumSq) * q.CVWeight
	}
	q.EligibleWindows = 1
	return q
}

func waitArea(arrivals []float64, start, end float64) float64 {
	var area float64
	for i := 1; i < len(arrivals); i++ {
		a := math.Max(start, arrivals[i-1])
		b := math.Min(end, arrivals[i])
		if b > a {
			area += (b - a) * (arrivals[i] - (a+b)/2)
		}
	}
	return area
}
