# Transit route performance

Transit pages and reports query PostgreSQL on each server request. Query
shape determines the cost: boolean flags need existence checks, counts
need grouped rows, and metrics read persisted rollups. See
[server reads](transit.md#server-reads) for the data flow and the separate
recorder, stream, planner, and HTTP cache state.

## Indexes and query semantics

| Read | Query | Supporting index |
| --- | --- | --- |
| Timetable cancellation flags | `EXISTS` for each trip within the requested feed day | Existing unique `cancellation(trip_id, feed_timestamp)` |
| Lifetime trip count | Group observations by date and trip, then count | Migration `000022`: `stop_delay(route_id, date, trip_id)` |
| Week selector | Check each of seven dates for observations or cancellations | Migration `000022`: `cancellation(route_id, start_date, trip_id)` |
| Stats reports | Aggregate recent observations by update time | Migration `000022`: `stop_delay(last_updated)` |

Timetables use the calendar day of `feed_timestamp`, including the audit
and legacy readers. Half-open timestamp bounds preserve the original
`feed_timestamp::date` rule in the database session's timezone, including
daylight-saving transitions. `EXISTS` avoids multiplying schedule rows
by repeated cancellation snapshots. The week selector continues to use
the cancellation's `start_date`.

Lifetime counts include every observed stop. First-stop counts and chunk
OTP counts would omit trips observed only downstream or without a
timepoint. The grouped query can use a covering index or hash aggregation;
an index-only scan is not guaranteed.

The new indexes are created atomically with the cleanup migration and add
index maintenance to recorder writes. The existing service-date
index on cancellation `(start_date, trip_id)` remains useful for other
readers. See [database indexes](database.md#index-strategy) for the full inventory.

## Restored production snapshot

Measured September 10, 2026 against baseline `617adf3`, using PostgreSQL
16.15 in local Docker. The restored database occupied 12 GB, with roughly
53.4 million vehicle positions, 3.8 million stop-delay observations, and
1.46 million cancellation snapshots.

The following route comparisons ran after `VACUUM ANALYZE` and after
background maintenance finished. Both versions used the same snapshot,
normal connection pool, real handlers and templates, and read-only connections.
The current version had migration 22's indexes installed. All temporary
indexes were removed afterward; the snapshot remains at migration 21.

Route dates were fixed to September 9 in `America/Thunder_Bay`. These are
five-request medians after the first request:

| Handler | Before | After |
| --- | ---: | ---: |
| Route 3C, full page | 470.06 ms | 18.97 ms |
| Route 3C, schedule body | 24.11 ms | 4.20 ms |
| Route 3C, schedule | 45.82 ms | 4.91 ms |
| Route 3C, `partial=1` | 52.50 ms | 10.70 ms |
| Route 1, full page | 477.80 ms | 18.56 ms |
| Route 1, schedule body | 5.38 ms | 4.73 ms |

The first route 3C request fell from 757.56 to 30.08 ms. PostgreSQL and OS
buffers were not flushed, so this is a first-handler-request measurement,
not a cold-disk measurement. Times include handler work and rendering, but
exclude network delivery and server middleware. They establish local
latency improvements, not production capacity under concurrent traffic.

Five-run SQL medians for route 3C, with the week selector covering
September 7–13:

| Read | Before | After | Effect |
| --- | ---: | ---: | --- |
| Timetable | 9.72 ms | 1.78 ms | 1,151 joined rows reduced to 64, preserving every distinct field |
| Lifetime trip count | 480.01 ms | 28.23 ms | Index-only scan; no heap fetches or disk spills |
| Week selector | 12.90 ms | 0.06 ms | Indexed existence checks |

The lifetime query reads 259,831 index entries and counts 8,521 trip/date
pairs. SQL times come from separate `EXPLAIN (ANALYZE, BUFFERS)` runs and
include its instrumentation overhead; they should not be added to the
handler timings. Bidirectional `EXCEPT ALL` checks found no differences
in trip counts, service dates, or distinct timetable rows.

Post-restore maintenance matters. Before vacuum, the restored tables had
no all-visible pages and the planner chose heap scans for lifetime counts.
The revised full route 3C page still improved from 526.97 to 208.96 ms in
that state. A normal vacuum made the covering index effective. That first
round overlapped automatic vacuuming and is retained only to explain the
difference, not as a clean capacity comparison. No planner overrides or
persistent PostgreSQL configuration changes were used.

The three new indexes occupied approximately 31.4 MiB (route observations),
10.4 MiB (route cancellations), and 28.1 MiB (delay update time) in this
snapshot. The timestamp index avoids millions of false-positive row
rechecks from the existing BRIN index. A plain btree was retained after
an earlier development comparison found little gain from a much larger
covering index.

Stats report comparisons also ran after maintenance, at the fixed cutoff
`2026-09-10 10:30:50-04` in `America/Thunder_Bay`:

| Stats report | Before | After |
| --- | ---: | ---: |
| Day snapshots | 976.44 ms | 330.90 ms |
| Percentiles | 351.95 ms | 27.21 ms |
| Week summary | 392.52 ms | 224.57 ms |

These are three-run SQL medians. Day snapshots deduplicate 366,534 vehicle
observations into 8,964 bucket/vehicle/route memberships before counting.
This replaces an 11 MB disk sort with an approximately 805 KiB memory sort.
The timestamp index selects 30,169 recent delays without the BRIN plan's
2.47 million irrelevant row rechecks. Percentile and week SQL is unchanged.

All three reports matched the baseline exactly both before and after adding
the index: 289 snapshots, 40 percentile buckets, and eight daily summaries.
Day and week reports still spend time scanning and aggregating recent
observations; these measurements do not imply that every endpoint now
finishes in a few milliseconds.

Every uncached request pays its SQL cost. In the initial snapshot checks,
seven-day cancellation details took about 19 ms and 180-day details about
326 ms; large ranges also carry larger responses. Stop analytics fell
from about 175 to 50 ms and live cancellation SQL from 15.5 to 9.3 ms,
with identical rows and no new indexes. These initial measurements
preceded the maintenance round above.

## Behavior checks

The production-snapshot route comparisons above covered six paths requested
six times per version, before and after maintenance: 144 responses in total.
Every response returned 200, with identical headers and complete HTML across
versions and repeated calls. These checks exercised the handlers directly;
they did not start a server, recorder, feed polling, or warmer. All temporary
comparison programs and response files were removed.

An earlier development-snapshot handler comparison covered 36 URLs requested
twice: route pages and all schedule partials, live, kiosk, method, audit,
metric and route-list ranges, APIs, redirects, and error responses.
All 72 responses matched
status and headers; all HTML and 70 complete bodies matched byte for
byte. The two timepoints API responses contained identical stops and
route/color pairs, ignoring the baseline's variable map iteration order.
That broader comparison predates the day-snapshot rewrite; current stats
results are checked separately by SQL comparisons and database fixtures.

SQL regression tests cover midnight and daylight-saving boundaries,
duplicate snapshots, trip counts, week boundaries, chunk updates, empty
data, and request cancellation. The live service test verifies that
successive reads see inserted and deleted data, duplicate snapshots
preserve trip and incident counts, and canceled or failed reads do not
return an earlier dashboard. Stats tests check fresh results after edits,
existing null/counting rules, and the atomic index migration up and down.
Fixed-snapshot comparisons cover their fixtures, not every production
state or the vehicle SSE stream.

## Index cleanup

Migration `000022` adds the three read indexes and removes 11 redundant
ones. This saves 164.87 MiB in the restored snapshot, or 95.05 MiB net after
the additions. The review
mapped current queries, projected columns, recipes, GTFS loading, and
constraints; restored `idx_scan` counters were not treated as production
usage evidence.

| Removed index | Replacement or reason |
| --- | --- | --- |
| `transit.idx_transit_stop_delay_route_stop_date` | Route/day access through the new route/date/trip index; tested OTP reads improved |
| `transit.idx_transit_stop_delay_service_date` | Route/date/trip also covers rollup date discovery |
| `transit.idx_transit_stop_delay_first_stop_band` | No current reader filters this flag |
| `transit.idx_transit_stop_delay_timepoint_band` | Recipes and exports resolve timepoints through `route_pattern_stop` |
| `transit.idx_transit_stop_delay_last_updated` | The new btree supplies selective update-time reads |
| `transit.idx_transit_cancellation_route_start` | Smaller full timestamp index; range-query plans were unchanged |
| `transit.idx_transit_route_pattern_route` | Leading key of the required route/headsign/direction unique index |
| `gtfs.idx_gtfs_calendar_dates_date` | GTFS projection reads calendar exceptions without a date lookup |
| `gtfs.idx_gtfs_stop_times_stop` | Raw schedule projections scan or join by trip; stop lookups use `transit` |
| `transit.idx_transit_scheduled_stop_first_dep` | No current reader filters on sequence one |
| `transit.idx_transit_scheduled_stop_tp_dep` | Current timepoint readers join schedules by trip |

The narrow `scheduled_stop_trip` index remains: five reader plans use it,
and replacing it with the wider primary key added about 2.5 ms to planner
reads. All primary and unique constraints, spatial access paths, and
foreign-key lookup coverage remain intact.

The event-index comparison covered 82 cases: all five chunk recipes, all
three bands, routes 3C and 1 on two dates, route reads, chunk details,
rollup discovery, cancellation ranges, and stats. Every result matched
with bidirectional `EXCEPT ALL`. A separate 14-query schedule/GTFS matrix
passed 28 comparisons across candidate sets. The removed service/date
index was useful for broad date discovery: its replacement kept the
180-day scan around 175 ms; an all-history scan measured 169.5 versus
197.3 ms with substantial run-to-run variation. This is a small background
scan tradeoff, rather than evidence that the index was never used.

The final handler comparison exercised 26 URLs: historical and current
routes, schedule partials, seven- and 180-day metrics/routes, live, kiosk,
stats variants, stops analytics, metadata, and timepoints. Each index set
received four sequential requests per URL plus four groups of twelve
requests at concurrency four: 304 responses across both sets. Query-error
tracing and populated-data checks guarded against successful but empty
responses. No SQL or rendering errors occurred, and data counts matched.
All headers and bodies matched byte for byte except the live page's existing
unordered hidden route-ID set, which matched after sorting that set alone.

| Final read | Serial median | Concurrent median / maximum |
| --- | ---: | ---: |
| Route 3C, full page | 16.26 ms | 30.10 / 50.11 ms |
| 180-day metrics | 286.13 ms | 588.00 / 731.87 ms |
| Day stats | 293.99 ms | 394.50 / 419.89 ms |

The proposed full index set measured 16.34, 281.32, and 304.35 ms for those
same serial reads. No material regression appeared after cleanup. These
remain local handler measurements with a fixed clock and no network or
recorder traffic; twelve concurrent samples per workload are a sanity check,
not a production capacity forecast. The 180-day metrics response is 4.70 MB
(8,466 chunks and 12,342 cancellation records), so delivery cost matters too.

Write costs were measured in disposable logged tables using 5,000 real
stop observations, three runs, and the recorder's conflict-update shape:

| Index set | Insert | Upsert | Upsert WAL |
| --- | ---: | ---: | ---: |
| Original production indexes | 24.89 ms | 66.50 ms | 2.92 MB |
| Original indexes plus new read indexes | 38.06 ms | 85.72 ms | 3.75 MB |
| Final reduced set | 22.63 ms | 58.46 ms | 2.99 MB |

This measures single SQL batches on freshly filled scratch tables, not
end-to-end recorder throughput or long-running update churn. Indexing
`last_updated` prevents HOT updates when that timestamp changes; unlike
a btree, a BRIN index does not impose that restriction in PostgreSQL 16.
See [PostgreSQL's HOT documentation](https://www.postgresql.org/docs/16/storage-hot.html).
The read benefit therefore still carries a maintenance cost. Removing the
old indexes reduces that cost without changing recorder timestamps or
deduplication rules.

The review also restored the former 30-second query limit for route
metadata, stops, and stop analytics while preserving caller cancellation.
Stats and live reads already retained that limit. A query-tracer regression
test verifies the deadlines without waiting for a timeout.

One existing stop-prediction fallback reader, `expectedRoutesAtStop`, could
not enter the index comparison: PostgreSQL rejects its `DISTINCT`/`ORDER BY`
combination, and its caller suppresses that error. This predates the cleanup
and remains unchanged; the required stop lookup index is retained.

## Validation

### Verify migrations

Migration 22 runs atomically during server startup. Its normal `CREATE INDEX`
statements take the usual table locks, so schedule it during a quiet window.
The listener waits for it. Startup logs migration failures and continues, so
an HTTP 200 does not prove the indexes are installed. After deployment, check
for a clean migration version of at least 22, all three new indexes valid, and
the eleven removed indexes absent:

```sql
SELECT version, dirty, version >= 22 AND NOT dirty AS ready
FROM public.schema_migrations;

SELECT required.name, COALESCE(i.indisvalid, false) AS valid
FROM (VALUES
  ('transit.idx_transit_stop_delay_route_date_trip'),
  ('transit.idx_transit_cancellation_route_date_trip'),
  ('transit.idx_transit_stop_delay_updated')
) AS required(name)
LEFT JOIN pg_index i ON i.indexrelid = to_regclass(required.name);

SELECT removed.name, to_regclass(removed.name) IS NULL AS removed
FROM (VALUES
  ('transit.idx_transit_stop_delay_route_stop_date'),
  ('transit.idx_transit_stop_delay_service_date'),
  ('transit.idx_transit_stop_delay_first_stop_band'),
  ('transit.idx_transit_stop_delay_timepoint_band'),
  ('transit.idx_transit_stop_delay_last_updated'),
  ('transit.idx_transit_cancellation_route_start'),
  ('transit.idx_transit_route_pattern_route'),
  ('gtfs.idx_gtfs_calendar_dates_date'),
  ('gtfs.idx_gtfs_stop_times_stop'),
  ('transit.idx_transit_scheduled_stop_first_dep'),
  ('transit.idx_transit_scheduled_stop_tp_dep')
) AS removed(name);
```

Expect one migration row with `ready = true` and three index rows with
`valid = true`, followed by eleven rows with `removed = true`.

### Measure a production snapshot

Restore into a separate local database and finish normal maintenance before
capturing baseline plans:

```bash
docker compose exec -T db psql -U postgres \
  -d thundercitizen_prod_snapshot -v ON_ERROR_STOP=1 \
  -c 'VACUUM (ANALYZE, PARALLEL 0);'
```

`ANALYZE` alone does not populate the visibility map that makes index-only
scans cheap. Serial vacuum also fits Docker's default small shared-memory
allocation. Wait for restore and maintenance workers to finish before timing.

Capture baseline SQL plans before installing the new indexes. Server startup
applies migrations and starts recorder and rollup writers, so start it on a
working copy after capturing the baseline. Then record complete-response timings:

```bash
go run ./cmd/perftest -n 10 -r
```

The report aggregates all requests per URL, measures the complete body,
and treats non-2xx or incomplete responses as errors. It does not flush
PostgreSQL buffers or compare older records. Use `-n 1` for a single request per URL.
See [development](development.md) for usage and saved-record details.

Compare the full route page and `?partial=schedule-body` for the same
route/date. Inspect [service.go](../internal/transit/service.go) and
[queries.go](../internal/transit/queries.go) with `EXPLAIN (ANALYZE, BUFFERS)`,
retaining first and repeated plans. Check heap fetches, disk sorts,
returned rows, and index usage. Also measure stats, live, seven-day and
180-day metrics, and route-list requests under expected concurrency.
If lifetime counts remain expensive, evaluate a per-route, per-day trip summary while
preserving all observed trips.

### Run regression tests

Run `go test ./...` and `go vet ./...` for repository checks. The PostgreSQL
tests use an administrative connection to create and drop unique scratch
databases. They do not modify the supplied database.

```bash
TRANSIT_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/thundercitizen?sslmode=disable' \
  go test -race ./internal/transit ./cmd/perftest -count=1 -v
```
