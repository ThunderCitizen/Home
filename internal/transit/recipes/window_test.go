package recipes

import (
	"math"
	"testing"
)

func TestWindowIntegrals(t *testing.T) {
	tests := []struct {
		name                string
		scheduled, observed []float64
		start, end, ewt     float64
	}{
		{"even", []float64{0, 600, 1200}, []float64{0, 600, 1200}, 0, 1200, 0},
		// Same irregular timetable must not create excess waiting.
		{"irregular timetable", []float64{0, 300, 1200}, []float64{0, 300, 1200}, 0, 1200, 0},
		{"bunched", []float64{0, 600, 1200}, []float64{0, 0, 1200}, 0, 1200, 300},
		{"negative retained", []float64{0, 300, 1200}, []float64{0, 600, 1200}, 0, 1200, -75},
		{"clipped band", []float64{0, 600, 1200}, []float64{0, 600, 1200}, 250, 950, 0},
		{"long gaps retained", []float64{0, 600, 12000}, []float64{0, 600, 12000}, 0, 12000, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := WindowStats(tc.scheduled, tc.observed, tc.start, tc.end)
			if q.WindowSeconds <= 0 {
				t.Fatal(q)
			}
			ewt := (q.WaitObservedArea - q.WaitScheduledArea) / q.WindowSeconds
			if math.Abs(ewt-tc.ewt) > 1e-9 {
				t.Fatalf("EWT=%v, want %v", ewt, tc.ewt)
			}
		})
	}
}
func TestWindowMissingBounds(t *testing.T) {
	q := WindowStats([]float64{0, 600, 1200}, []float64{1800, 2400, 3000}, 0, 3600)
	if q.WindowSeconds != 0 || q.EligibleWindows != 0 {
		t.Fatal(q)
	}
}
