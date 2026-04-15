# Database

## Migrations

```bash
# Run migrations (requires DATABASE_URL)
DATABASE_URL="postgres://..." make migrate-up
DATABASE_URL="postgres://..." make migrate-down
```

Migrations auto-run on server startup.

## Schema

Migrations in `migrations/`. Relations live in named Postgres schemas:

- **`gtfs.*`** — raw GTFS schedule imports (`routes`, `stops`, `trips`, `stop_times`, `calendar`, `calendar_dates`, `shapes`, `transfers`, `feed_info`)
- **`transit.*`** — transit domain (events, derived state, schedule projections):
  - **GTFS-RT events** — `transit.vehicle_position`, `transit.stop_delay`, `transit.cancellation`, `transit.alert`
  - **Derived** — `transit.stop_visit` (GPS proximity detection), `transit.route_band_chunk` (metric rollups — see below)
  - **Schedule projections** — `transit.route`, `transit.route_pattern`, `transit.route_pattern_stop`, `transit.route_baseline`, `transit.scheduled_stop`, `transit.service_calendar`, `transit.stop`, `transit.trip_catalog`
  - **Fleet tracking** — `transit.vehicle`, `transit.vehicle_assignment`
  - **Operational** — `transit.feed_state`, `transit.feed_gap`
- **`public.*`** — everything else: `councillors`, `council_meetings`, `council_motions`, `council_vote_records`, `budget_accounts`, `budget_ledger`
- **Operational public tables** — `data_patch_log` (muni bundle apply audit: dataset checksum + signer + timestamp), `muni_fetch_state` (single-row throttle, migration `000007`)

## PostGIS

The `db` container uses `Dockerfile.db` (Debian + `postgresql-16-postgis-3`). Geography columns on `transit.stop` and `transit.vehicle_position` enable spatial queries:

- **Nearest stops** — KNN via `<->` operator on GiST index
- **Vehicle-to-stop distance** — `ST_Distance` between geography columns
- Triggers auto-populate `geog` on INSERT/UPDATE — no changes to write paths

### Index Strategy

The original schema had ~1.1 GB of indexes for ~500 MB of table data — several
were never used (GIS on vehicle positions, per-vehicle history) and one btree
on `last_updated` was 386 MB alone for 18 scans. The current set targets actual
query patterns and dropped total index footprint to ~170 MB.

**transit.stop_delay** (heaviest table, ~357K rows)

| Index | Type | Covers |
|-------|------|--------|
| PK `(date, trip_id, stop_id)` | btree | OTP date-range scans, trip delay lookups |
| `idx_transit_stop_delay_route_stop_date` | btree | Per-route per-stop metrics |
| `idx_transit_stop_delay_last_updated` | BRIN | 24h dashboard percentile queries (24 KB vs 386 MB btree) |
| `idx_transit_stop_delay_first_stop_band` | btree (partial: is_first_stop) | Per-band cancel/OTP at trip start |
| `idx_transit_stop_delay_timepoint_band` | btree (partial: is_timepoint) | Per-band EWT timepoint queries |
| `idx_transit_stop_delay_service_date` | btree | Service-day rollups |

**transit.stop_visit** (~180K rows)

| Index | Type | Covers |
|-------|------|--------|
| PK `(trip_id, stop_id)` | btree | Upsert on write path |
| `idx_transit_stop_visit_route_stop INCLUDE (observed_at)` | btree | Headway/EWT/Cv — covering index for index-only scans |
| `idx_transit_stop_visit_observed` | btree | Date-range headway window functions |

**transit.cancellation** (~44K rows)

| Index | Type | Covers |
|-------|------|--------|
| UNIQUE `(trip_id, feed_timestamp)` | btree | Dedup on insert, cancel detail queries |
| `idx_transit_cancellation_feed_timestamp` | btree | Date-range cancel rate scans |
| `idx_transit_cancellation_route_start` | btree (partial) | Cancel detail GROUP BY (WHERE start_time IS NOT NULL) |

**transit.vehicle_position** (~2.8M rows)

| Index | Type | Covers |
|-------|------|--------|
| PK `(id)` | btree | Required |
| `idx_transit_vehicle_position_feed_timestamp` | btree | 24h dashboard, live feed queries |

**Other tables**

| Index | Table | Covers |
|-------|-------|--------|
| `idx_transit_alert_feed_timestamp` | `transit.alert` | Latest-alert queries |
| GiST on `geog` column | `transit.stop` | PostGIS KNN nearest-stop queries |
| `idx_gtfs_stop_times_stop` | `gtfs.stop_times` | Stop-level schedule queries |
| `idx_data_patch_log_patch_id` | `public.data_patch_log` | Latest-apply lookup per dataset (for muni drift check) |

### Schedule-headway computation

EWT and related scheduled-headway calculations are derived inline from
`gtfs.stop_times` joined against `transit.route_baseline` (the per-route
timepoint projection) and the (service_id, date) pairs we observed running
(via `transit.stop_delay`). The previous materialized sched_headways view
was dropped — it depended on calendar_dates which silently lapsed on
long-lived deployments whenever the GTFS bundle's coverage rolled past
the queried date range. See the `headway` recipe in
`internal/transit/recipes/` and the chunk orchestrator in
`internal/transit/chunk.go`.

### Metric rollup table — `transit.route_band_chunk`

The chunk-based metrics read path stores one row per (route, date, band)
in `transit.route_band_chunk` (added in migration `000003`, formerly
`transit.route_band_bucket`). Columns are raw counts plus SUM-stable
headway sums (`headway_sum_sec`, `headway_sum_sec_sq`, `sched_headway_sec`),
never percentages — aggregation happens in Go via `KPIFromChunks` in
`internal/transit/view_helpers.go` and the matching JS port in
`static/transit/chunks.js`. The orchestrator that fills this table is
`BuildChunksForDate` in `internal/transit/chunk.go`, which calls five
per-metric recipes from `internal/transit/recipes/` against the upstream
event tables.

Kept populated automatically by `ChunkRollup` (`internal/transit/chunk_rollup.go`):
a background goroutine that does a 60-day backfill on boot and rebuilds
today's chunks every 10 minutes. See [docs/transit-metrics.md](transit-metrics.md)
for the full write-path + failure-mode story.

### Postgres Tuning

| Setting | Default | Current | Why |
|---------|---------|---------|-----|
| `work_mem` | 4 MB | 16 MB | Eliminates disk-spill sorts in headway window functions |
| `shared_buffers` | 128 MB | 256 MB | Keeps hot tables (`transit.stop_visit`, `transit.stop_delay`) in memory |

## Connection

Uses `pgx/v5` with connection pooling. Pool configured in `internal/database/db.go`:

- Max connections: 25
- Min connections: 5
- Max lifetime: 1 hour
- Max idle time: 30 minutes
- **`DefaultQueryExecMode = QueryExecModeCacheDescribe`** — caches parameter
  type descriptions (fast protocol) but re-plans every query. The default
  `QueryExecModeCacheStatement` caches the full prepared plan, and after 5
  executions Postgres switches from a "custom plan" (replanned with actual
  parameter values) to a "generic plan" (planned once with no parameter
  info). For the per-band metric queries with selective `departure_time`
  range filters, the generic plan picks a pathological join order and the
  same query that runs in 150 ms takes 30+ seconds. Re-planning every call
  is cheap relative to the actual work the query does; see the
  `internal/database/db.go` comment for the incident history.

## Data Loading at Startup

`cmd/server/main.go` loads data after migrations:

1. `transit.LoadStaticGTFS(ctx, db)` — Routes, stops, trips, stop_times, calendar_dates loaded into `gtfs.*` from the GTFS CSV files, then projected into `transit.route`, `transit.route_pattern`, `transit.route_pattern_stop`, `transit.route_baseline`, `transit.scheduled_stop`, `transit.service_calendar`, `transit.stop`, `transit.trip_catalog`. After loading, this also:
   - Derives display names from headsigns where `long_name` is empty
   - Runs `ANALYZE` on the freshly-loaded tables so the planner has
     fresh statistics. Bulk loads don't trigger autoanalyze, and stale
     stats caused the per-band metric queries to pick pathological
     seq-scan plans.
2. `data.LoadFIRFromDB(ctx, db)` — FIR budget data, merged into `BudgetByYear`
