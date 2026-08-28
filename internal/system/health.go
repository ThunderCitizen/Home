package system

import (
	"fmt"
	"strings"
	"time"

	"thundercitizen/internal/system/metrics"
	"thundercitizen/internal/transit"
)

// HealthViewModel drives the /health page. Built from the metrics
// snapshot plus container-baked env (TC_IMAGE) and ldflag-baked build
// metadata passed in by the handler.
type HealthViewModel struct {
	Image     string
	Commit    string
	BuildTime string
	BootAt    time.Time
	Uptime    time.Duration

	P50        time.Duration
	P90        time.Duration
	P99        time.Duration
	HasLatency bool
	Routes     []RouteLatency

	GTFSFeeds []FeedHealth
}

// RouteLatency holds count-free response-time percentiles for one route.
type RouteLatency struct {
	Pattern string
	P50     time.Duration
	P90     time.Duration
}

// FeedHealth is the template-facing representation of one GTFS-RT feed's
// current polling health. Built from transit.FeedStats in NewHealthViewModel
// so the template doesn't have to compute status strings or relative times.
type FeedHealth struct {
	Name             string // display: "Vehicles", "Trips", "Alerts"
	Interval         string // e.g. "6s", "60s"
	Status           string // "ok" | "stale" | "error" | "pending"
	StatusLabel      string // display: "OK", "Stale", "Error", "Waiting"
	LastSuccessLabel string // "4s ago", "—" if never
	LastFeedTSLabel  string // upstream feed timestamp, human-readable
	LastError        string
	SuccessCount     uint64
	ErrorCount       uint64
}

// NewHealthViewModel composes the view model. Pass the image string
// and build metadata in so this package doesn't reach back into env or
// the handlers package.
func NewHealthViewModel(image, commit, buildTime string, feeds []transit.FeedStats) HealthViewModel {
	snap := metrics.Read()

	p50 := metrics.Percentile(snap.GlobalLatency[:], 50)
	p90 := metrics.Percentile(snap.GlobalLatency[:], 90)
	p99 := metrics.Percentile(snap.GlobalLatency[:], 99)
	routes := make([]RouteLatency, 0, len(snap.Routes))
	for _, r := range snap.Routes {
		routeP90 := metrics.Percentile(r.Latency[:], 90)
		if routeP90 == 0 {
			continue
		}
		routes = append(routes, RouteLatency{
			Pattern: r.Pattern,
			P50:     metrics.Percentile(r.Latency[:], 50),
			P90:     routeP90,
		})
	}

	return HealthViewModel{
		Image:      image,
		Commit:     commit,
		BuildTime:  buildTime,
		BootAt:     snap.BootAt,
		Uptime:     snap.Uptime,
		P50:        p50,
		P90:        p90,
		P99:        p99,
		HasLatency: p90 > 0,
		Routes:     routes,

		GTFSFeeds: buildFeedHealth(feeds),
	}
}

// buildFeedHealth converts raw recorder snapshots into template-friendly
// rows. Status thresholds: a feed is "stale" if its last successful fetch
// is older than 3× its expected poll interval, "error" if the most recent
// attempt failed after any successful fetches, "pending" if it hasn't
// logged a success yet (fresh boot).
func buildFeedHealth(feeds []transit.FeedStats) []FeedHealth {
	if len(feeds) == 0 {
		return nil
	}
	now := time.Now().UTC()
	out := make([]FeedHealth, 0, len(feeds))
	for _, f := range feeds {
		row := FeedHealth{
			Name:         feedDisplayName(f.FeedType),
			Interval:     FormatDuration(f.Interval),
			SuccessCount: f.SuccessCount,
			ErrorCount:   f.ErrorCount,
			LastError:    f.LastError,
		}
		switch {
		case f.LastSuccessAt.IsZero() && f.LastErrorAt.IsZero():
			row.Status = "pending"
			row.StatusLabel = "Waiting"
			row.LastSuccessLabel = "—"
			row.LastFeedTSLabel = "—"
		case f.LastErrorAt.After(f.LastSuccessAt):
			row.Status = "error"
			row.StatusLabel = "Error"
			row.LastSuccessLabel = formatSince(now, f.LastSuccessAt)
			row.LastFeedTSLabel = formatSince(now, f.LastFeedTS)
		case !f.LastSuccessAt.IsZero() && now.Sub(f.LastSuccessAt) > 3*f.Interval:
			row.Status = "stale"
			row.StatusLabel = "Stale"
			row.LastSuccessLabel = formatSince(now, f.LastSuccessAt)
			row.LastFeedTSLabel = formatSince(now, f.LastFeedTS)
		default:
			row.Status = "ok"
			row.StatusLabel = "OK"
			row.LastSuccessLabel = formatSince(now, f.LastSuccessAt)
			row.LastFeedTSLabel = formatSince(now, f.LastFeedTS)
		}
		out = append(out, row)
	}
	return out
}

func feedDisplayName(t string) string {
	switch t {
	case "vehicles":
		return "Vehicle positions"
	case "trips":
		return "Trip updates"
	case "alerts":
		return "Service alerts"
	}
	return t
}

func formatSince(now, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	return FormatDuration(d) + " ago"
}

// FormatDuration renders a Duration as a compact "12ms" / "340µs" /
// "1.4s" string for the health page.
func FormatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "–"
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

// FormatUptime renders a Duration as a compact "1d 4h 12m 3s" string.
// Drops zero-value leading fields so a fresh boot shows "12s" rather
// than "0d 0h 0m 12s".
func FormatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int(d / time.Second)

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 || len(parts) > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 || len(parts) > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	parts = append(parts, fmt.Sprintf("%ds", secs))
	return strings.Join(parts, " ")
}
