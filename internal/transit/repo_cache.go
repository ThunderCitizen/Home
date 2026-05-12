package transit

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// liveTTL is how long the live dashboard bundle stays cached before the
// next Get triggers a re-load. Everything else in RepoCache caches forever
// (or until the key changes).
const liveTTL = 30 * time.Second

// RepoCache is the single cache layer that sits over the Repository for the
// transit service. Every cached data product is a field on this struct —
// either a CacheSlot (scalar value) or a CacheMap (keyed value). Loaders are
// set up in NewRepoCache and close over the Reporter to reach the DB.
//
// Design notes:
//
//   - Strategy is the simplest thing that works: lazy-load on cold read,
//     cache forever thereafter. No background warmers, no periodic
//     refresh. The first caller after boot pays the compute cost; every
//     subsequent caller hits memory.
//   - The `live` slot is the one exception — it has a 30s TTL (see
//     liveTTL) so dashboard data (alerts, cancellations, fleet) stays
//     fresh. The next Get after TTL expiry triggers a re-load.
//   - Historical caches have no eviction. Keyed caches (metrics, stats,
//     stopMetrics) grow over time as distinct keys are queried. Today's
//     current-week entry gets naturally rotated when the date-range key
//     changes at midnight.
//   - The `kpiCache` from the old design is gone — KPIs are pure string
//     formatting over the metrics slot's output and get derived on every
//     read. See formatSystemKPIs / formatRouteKPIs in metrics.go.
type RepoCache struct {
	// Historical scalar caches (no key, single value).
	routeMeta     *CacheSlot[[]RouteMetaAPI]
	allStops      *CacheSlot[[]Stop]
	stopAnalytics *CacheSlot[[]StopAnalyticsRow]

	// Stats keyed cache (day / percentiles / week — for the cancel log
	// percentile chart on the metrics tab).
	stats *CacheMap[string, *StatsReport]

	// Live slot — 30s TTL. The only slot in RepoCache with an expiry;
	// the next Get after expiry re-loads from the DB. Everything else
	// caches forever.
	//
	// NOTE: metrics data does NOT live here. The chunk cache
	// (Service.ChunkCache, internal/transit/chunk_cache.go) holds chunks
	// and is the only thing that talks to transit.route_band_chunk.
	// RepoCache holds everything that isn't a chunk.
	live *CacheSlot[*liveData]
}

// NewRepoCache wires up every cache slot with a loader that captures the
// Reporter. The loaders are where "cache over the repository layer" actually
// lives — each is a thin adapter that knows how to fetch one data product.
func NewRepoCache(reporter *Reporter) *RepoCache {
	db := reporter.db

	return &RepoCache{
		routeMeta: NewCacheSlot("route-meta", func(ctx context.Context) ([]RouteMetaAPI, error) {
			to := ServiceDate()
			from := to.AddDate(0, 0, -6)
			return reporter.repo.AllRouteMeta(ctx, from, to)
		}),

		allStops: NewCacheSlot("all-stops", func(ctx context.Context) ([]Stop, error) {
			return reporter.AllStopsReport(ctx)
		}),

		stopAnalytics: NewCacheSlot("stop-analytics", func(ctx context.Context) ([]StopAnalyticsRow, error) {
			return reporter.repo.StopAnalytics(ctx, 7)
		}),

		stats: NewCacheMap("stats", func(ctx context.Context, variant string) (*StatsReport, error) {
			switch variant {
			case "day":
				return reporter.DayStats(ctx)
			case "percentiles":
				return reporter.Percentiles(ctx)
			case "week":
				return reporter.WeekStats(ctx)
			}
			return nil, fmt.Errorf("unknown stats variant %q", variant)
		}),

		live: NewCacheSlotTTL("live-data", liveTTL, func(ctx context.Context) (*liveData, error) {
			// All three loaders are independent at the DB layer. Run them
			// concurrently so wall-clock collapses to the slowest single
			// query instead of the sum.
			var (
				dashboard *DashboardReport
				incidents []CancelIncident
				noSvc     []string
			)
			today := ServiceDate()
			g, gctx := errgroup.WithContext(ctx)
			g.Go(func() error {
				v, err := reporter.Dashboard(gctx)
				if err != nil {
					return err
				}
				dashboard = v
				return nil
			})
			g.Go(func() error {
				v, err := reporter.CancelIncidents(gctx)
				if err != nil {
					return fmt.Errorf("incidents: %w", err)
				}
				incidents = v
				return nil
			})
			g.Go(func() error {
				v, err := NoServiceRoutes(gctx, db, today)
				if err != nil {
					return fmt.Errorf("no-service routes: %w", err)
				}
				noSvc = v
				return nil
			})
			if err := g.Wait(); err != nil {
				return nil, err
			}
			// CancelIncidents' schedule-walk can return empty when trip IDs
			// don't line up with static GTFS. Fall back to the cancellation
			// details Dashboard already loaded — avoids a duplicate query.
			if len(incidents) == 0 && dashboard != nil {
				incidents = IncidentsFromDetails(dashboard.CancelledTrips)
			}
			return &liveData{
				dashboard: dashboard,
				incidents: incidents,
				noService: noSvc,
			}, nil
		}),
	}
}
