package chunk

import (
	"math"
	"testing"
	"time"
)

func almostEqual(t *testing.T, name string, got, want, tolerance float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tolerance {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}
func TestCv(t *testing.T) {
	almostEqual(t, "5,7,3,10,5 minutes", Cv(5, 1800, 748800), 0.3944053189, 1e-8)
	almostEqual(t, "uniform", Cv(4, 1200, 360000), 0, 1e-12)
	almostEqual(t, "bunching retained", Cv(3, 900, 810000), math.Sqrt(2), 1e-12)
}
func TestKPIWeightedCountsAndMatchedWindows(t *testing.T) {
	rows := []ChunkView{
		{RouteID: "A", Band: "morning", Trips: 1, Scheduled: 10, Cancelled: 2, NoNotice: 1, Quality: Quality{Version: Version, OTPCount: 10, OTPOnTime: 5, WaitObservedArea: 72000, WaitScheduledArea: 36000, WindowSeconds: 600, CVWeightedSum: 120, CVWeight: 600}},
		{RouteID: "A", Band: "evening", Trips: 9, Scheduled: 90, Cancelled: 9, NoNotice: 3, Quality: Quality{Version: Version, OTPCount: 90, OTPOnTime: 90, WaitObservedArea: 108000, WaitScheduledArea: 144000, WindowSeconds: 1200, CVWeightedSum: 0, CVWeight: 1200}},
		{RouteID: "B", Band: "morning", Quality: Quality{Version: Version, WaitObservedArea: 72000, WaitScheduledArea: 36000, WindowSeconds: 600, CVWeightedSum: 300, CVWeight: 600}},
		// An old recipe must never enter corrected rates or trends.
		{RouteID: "A", Trips: 100, Scheduled: 100, Cancelled: 100},
	}
	for _, tc := range []struct {
		metric Metric
		want   float64
	}{{MetricOTP, 95}, {MetricCancel, 11}, {MetricNotice, 400.0 / 11}, {MetricEWT, .5}, {MetricCv, (120.0/1800 + .5) / 2}, {MetricWait, 252000.0 / 2400 / 60}} {
		got, ok := KPI(rows, tc.metric, "")
		if !ok {
			t.Fatal("missing", tc.metric)
		}
		almostEqual(t, string(tc.metric), got, tc.want, 1e-9)
	}
	got, _ := KPI(rows, MetricOTP, "morning")
	almostEqual(t, "band OTP", got, 50, 1e-9)
	got, _ = KPI(rows[1:2], MetricEWT, "")
	almostEqual(t, "negative EWT", got, -.5, 1e-9)
}
func TestKPIMissingIsNotZero(t *testing.T) {
	for _, m := range []Metric{MetricOTP, MetricCancel, MetricNotice, MetricWait, MetricEWT, MetricCv} {
		if _, ok := KPI([]ChunkView{{Quality: Quality{Version: Version}, Scheduled: 100}}, m, ""); ok {
			t.Errorf("%s fabricated data", m)
		}
	}
	// Constant, well observed spacing is a legitimate zero CV.
	if got, ok := KPI([]ChunkView{{Quality: Quality{Version: Version, CVWeight: 600}}}, MetricCv, ""); !ok || got != 0 {
		t.Fatal(got, ok)
	}
}
func TestChunkViewUsesSameFormula(t *testing.T) {
	c := Chunk{RouteID: "7", Date: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), ServiceKind: "saturday", Quality: Quality{Version: Version, OTPCount: 10, OTPOnTime: 8, WaitObservedArea: 60000, WaitScheduledArea: 30000, WindowSeconds: 500, CVWeightedSum: 100, CVWeight: 500}}
	v := c.View()
	for _, tc := range []struct {
		metric  Metric
		display *float64
	}{{MetricOTP, v.OTPPct}, {MetricEWT, v.EWTMin}, {MetricCv, v.Cv}} {
		got, ok := KPI([]ChunkView{v}, tc.metric, "")
		if !ok {
			t.Fatal(tc.metric)
		}
		almostEqual(t, string(tc.metric), got, *tc.display, 1e-12)
	}
	if v.Date != "2026-08-01" || v.ServiceKind != "saturday" {
		t.Fatal(v)
	}
}

func TestEmptyViewUsesNullReadings(t *testing.T) {
	v := (Chunk{}).View()
	if v.OTPPct != nil || v.EWTMin != nil || v.Cv != nil {
		t.Fatal(v)
	}
}
