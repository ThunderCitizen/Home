# Database

## Migrations

```bash
# Run migrations (requires DATABASE_URL)
DATABASE_URL="postgres://..." make migrate-up
DATABASE_URL="postgres://..." make migrate-down
```

Migrations auto-run on server startup. `000022` is the complete transit
performance index migration: it adds the read indexes and removes redundant
ones in one atomic cleanup.
Follow the [deployment verification steps](route-performance.md#validation).

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
- **Operational public tables** — `data_patch_log` (muni bundle apply audit: dataset checksum + signer + timestamp), `muni_fetch_state` (last successful production bundle check, migration `000007`)

## PostGIS

The `db` container uses `Dockerfile.db` (Debian + `postgresql-16-postgis-3`). Geography columns on `transit.stop` and `transit.vehicle_position` enable spatial queries:

- **Nearest stops** — KNN via `<->` operator on GiST index
- **Vehicle-to-stop distance** — `ST_Distance` between geography columns
- Triggers auto-populate `geog` on INSERT/UPDATE — no changes to write paths

### Index Strategy

The transit index set was checked against a restored 12 GB production
snapshot and current readers, recorder writes, metric recipes, and GTFS
projections. Index scan counters on a restored database describe local tests;
they do not show production usage. Primary keys, unique constraints, and
foreign-key lookup coverage remain intact.

Migration `000022` removes 11 secondary indexes, about 165 MiB in
the snapshot. Including the three new read indexes, the final set is about
95 MiB smaller than the original production set. Each migration contains
one atomic cleanup operation, and its down migration restores the original
definitions. Details and tradeoffs are in
[the performance review](route-performance.md#index-cleanup).

**transit.stop_delay** (about 3.8 million rows; three indexes after cleanup)

| Index | Type | Covers |
|-------|------|--------|
| PK `(date, trip_id, stop_id)` | btree | Recorder upserts, date-range scans, trip/stop delay lookups |
| `idx_transit_stop_delay_route_date_trip` | btree | Route/day reads, lifetime trip counts, OTP, and rollup date discovery |
| `idx_transit_stop_delay_updated` | btree | Recent update-time ranges for stats reports |

Timepoint membership comes from `route_pattern_stop`; readers do not filter
the recorder's `stop_delay.is_timepoint` flag. The old band indexes and BRIN
are removed, as are the route/stop/date and service/date indexes replaced
by the route/date/trip access path.

**transit.stop_visit** (about 825,000 rows)

| Index | Type | Covers |
|-------|------|--------|
| PK `(trip_id, stop_id)` | btree | Recorder conflict handling |
| `idx_transit_stop_visit_route_stop INCLUDE (observed_at)` | btree | Legacy visit queries; covering observed-time reads |
| `idx_transit_stop_visit_observed` | btree | Recent visit analytics |

**transit.cancellation** (about 1.46 million rows)

| Index | Type | Covers |
|-------|------|--------|
| PK `(id)` | btree | Row identity |
| UNIQUE `(trip_id, feed_timestamp)` | btree | Insert deduplication and timetable cancellation checks |
| `idx_transit_cancellation_feed_timestamp` | btree | Feed-time ranges and recent cancellation counts |
| `idx_transit_cancellation_start_date` | btree, covering | Cancellation queries by service date |
| `idx_transit_cancellation_route_date_trip` | btree | Route/day existence checks and cancellation counts |

The removed `route_start` index was actually feed-timestamp-leading. Its
wide partial key did not improve the tested range readers over the smaller
full timestamp index.

**transit.vehicle_position** (about 53.4 million rows)

| Index | Type | Covers |
|-------|------|--------|
| PK `(id)` | btree | Row identity; retained despite its size |
| `idx_transit_vehicle_position_feed_timestamp` | btree | Recent vehicle observations and dashboard reads |

**Other retained access paths**

| Index | Table | Covers |
|-------|-------|--------|
| Alert ID/feed-time uniqueness + timestamp index | `transit.alert` | Recorder deduplication and latest/recent alerts |
| GiST on `geog` | `transit.stop` | PostGIS nearest-stop lookup |
| `idx_transit_scheduled_stop_stop` | `transit.scheduled_stop` | Stop lookups and foreign-key maintenance |
| `idx_transit_scheduled_stop_trip` | `transit.scheduled_stop` | Narrow trip-join index used by schedule, planner, and audit readers |
| PK `(trip_id, stop_sequence)` | `gtfs.stop_times` | GTFS trip joins and ordered schedules |
| `idx_transit_service_calendar_date` | `transit.service_calendar` | Active-service dates |
| `idx_data_patch_log_patch_id` | `public.data_patch_log` | Latest-apply lookup per dataset |

Leading-column overlap alone does not make an index dispensable. The narrow
`scheduled_stop_trip` index measurably helps current readers, so it remains
alongside the wider primary key. The raw GTFS stop/date indexes and legacy
scheduled-departure indexes had no remaining reader or constraint role.

### Historical metric evidence

Migration 28 retains timetable versions in `metric_schedule`, active calendar
entries in `metric_service` and ordered trip stops in `metric_trip`.
`metric_passage` stores screened GPS passage estimates, schedule/detector
versions and source GPS IDs. `metric_rebuild` records the recipe version,
timetable and build time for each completed rollup date.

`stop_visit` is now keyed by `(service_date, trip_id, stop_id)`; its service
date is generated from the observed timestamp with a 04:00 local boundary.
Existing duplicate local records are preserved in `stop_visit_duplicate_archive`.
The down migration refuses a lossy rollback; retain a pre-migration backup.

### Metric rollup table — `transit.route_band_chunk`

One row per route × date × band stores raw sample counts, coverage, matched
wait integrals and within-window CV contributions. These are combined by
`chunk.KPI` and the tested JavaScript reducer, never by averaging rounded
percentages. Legacy headway/trip-average columns remain for compatibility,
but version 0 rows are excluded from corrected readings.

`metric_day.go` reads each source once per date and `BuildChunksForDate`
replaces that date in a transaction. `ChunkRollup` refreshes today every
10 minutes, finalizes yesterday after the service day closes, and scans
60 recent days plus older invalidated chunks. Only dates covered by an
archived timetable are rebuilt. See [transit metrics](transit-metrics.md)
for the definitions and [validation audit](transit-metric-audit.md) for the
historical results.

### Postgres settings

The Compose files do not set `work_mem` or `shared_buffers`. The local
production-snapshot measurements used 4 MB and 128 MB respectively; a dump
does not carry the production server's configuration. Inspect the target
server rather than assuming those values apply everywhere:

```sql
SHOW work_mem;
SHOW shared_buffers;
SHOW max_wal_size;
```

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

Transit reports query PostgreSQL on each request. The pool and parameter
description cache reuse connections and protocol metadata; they do not
retain result rows. See [server reads](transit.md#server-reads) for the
application's data flow.

## Data Loading at Startup

`cmd/server/main.go` loads data after migrations:

1. `transit.LoadStaticGTFS(ctx, db)` — Routes, stops, trips, stop_times, calendar_dates loaded into `gtfs.*` from the GTFS CSV files, then projected into `transit.route`, `transit.route_pattern`, `transit.route_pattern_stop`, `transit.route_baseline`, `transit.scheduled_stop`, `transit.service_calendar`, `transit.stop`, `transit.trip_catalog`. After loading, this also:
   - Derives display names from headsigns where `long_name` is empty
   - Runs `ANALYZE` on the freshly-loaded tables so the planner has
     fresh statistics. Bulk loads don't trigger autoanalyze, and stale
     stats caused the per-band metric queries to pick pathological
     seq-scan plans.
2. `data.LoadFIRFromDB(ctx, db)` — FIR budget data, merged into `BudgetByYear`
