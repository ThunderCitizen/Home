package transit

import (
	"context"
	"time"

	"thundercitizen/internal/logger"
)

// LiveWarmer keeps the Service's live slot fresh in the background so
// /transit page requests never pay the loader cost on the hot path.
//
// The live slot has a 30s TTL (see liveTTL in repo_cache.go). Without a
// warmer, the first request after each expiry runs the loader (Dashboard
// + CancelIncidents + NoServiceRoutes — three DB round-trips in parallel)
// synchronously, spiking that request's latency. The warmer ticks faster
// than the TTL and calls Refresh, so the slot is rebuilt before it can
// expire and Get always sees a hot value.
type LiveWarmer struct {
	svc      *Service
	interval time.Duration
}

var liveWarmerLog = logger.New("live_warmer")

// NewLiveWarmer returns a warmer that refreshes every 20s — comfortably
// under the 30s TTL so a slow loader doesn't let the slot expire between
// ticks.
func NewLiveWarmer(svc *Service) *LiveWarmer {
	return &LiveWarmer{svc: svc, interval: 20 * time.Second}
}

// Start launches the warmer goroutine. Returns immediately; the goroutine
// exits when ctx is cancelled. Does an initial refresh up front so the
// first /transit request after boot is also warm.
func (w *LiveWarmer) Start(ctx context.Context) {
	go w.run(ctx)
}

func (w *LiveWarmer) run(ctx context.Context) {
	// Pre-warm the default /transit/metrics window (last 7 days) so the
	// first hit after boot is hot. parseDateRange's default is the same
	// window, keyed off Today() — re-derive it here.
	today := Today()
	from := today.AddDate(0, 0, -6)
	warmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := w.svc.WarmCancelDetails(warmCtx, from, today); err != nil {
		liveWarmerLog.Warn("default range pre-warm failed", "err", err)
	}
	cancel()

	w.refresh(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.refresh(ctx)
		}
	}
}

func (w *LiveWarmer) refresh(ctx context.Context) {
	tickCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := w.svc.RefreshLive(tickCtx); err != nil {
		// Slot keeps serving its previous value until the next successful
		// refresh; Get will only re-run the loader if the value also ages
		// past the TTL before another tick succeeds.
		liveWarmerLog.Warn("refresh failed", "err", err)
	}
	// Refresh any cancel-detail range that ends today so /transit/metrics
	// stays current. Ranges that don't include today are immutable and
	// don't need re-loading.
	if err := w.svc.RefreshTodayCancelDetails(tickCtx); err != nil {
		liveWarmerLog.Warn("cancel-details refresh failed", "err", err)
	}
}
