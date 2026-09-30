# Transit metrics: definitions, provenance and rebuilding

The Metrics page reports **sampled service performance**, not verified passenger outcomes. The raw feeds are retained, but their presence alone does not make every derived measure valid. Migration 28 and recipe version 1 replace the earlier trip-average OTP and pooled-headway calculations.

## What changed

- Visits were keyed by `(trip_id, stop_id)`. GTFS trip IDs recur on multiple dates, so later visits conflicted with earlier records. The recorder now keys by service date as well; exit updates identify the original entry timestamp.
- The old detector considered every stop on a route, including the opposite direction, interpolated across arbitrarily long outages, and never retried an initial cache-load failure. It now uses the trip's scheduled stops, limits interpolation gaps and refreshes successful caches.
- Historical metrics depended on replaceable current schedule tables. Timetables are now archived by content hash, with calendars and ordered per-trip stops.
- OTP averaged a trip's early and late timepoints before classification. Each departure timepoint now receives its own classification.
- Observed headways below a minute or above two hours were discarded, removing real bunching and disruption. The new calculation screens observations, not headway magnitudes.
- Scheduled wait was half a mean headway, even for irregular timetables; negative EWT was clamped to zero. Both waits now use the same bounded interval, and signed differences are retained.
- CV pooled different stops, bands and dates, manufacturing variation when scheduled frequency changed. CV is computed within each stop/direction/day/band window before combining results.
- Existing rows were treated as proof that an entire date was finished. A transaction now replaces the whole date and records its recipe version; yesterday's unfinished build is finalized after the service day closes.
- Chart nulls became zeros. Missing and legacy values now remain unavailable; the trend has an explicit completeness rule and coverage strip.

## Definitions

### Reported timepoint punctuality

One sample is a retained delay for a scheduled timepoint departure, excluding the trip's final destination and repeated stop IDs. Departure delay takes precedence; arrival delay is a fallback. The selected window is -60 through +300 seconds, inclusive. Count early/on-time/late samples separately; sum counts before division. Band membership uses that stop's scheduled departure, not the trip's first stop.

`OTP = 100 × Σotp_on_time / Σotp_count`

This is **feed-reported punctuality**. The trip-update feed can contain predictions, and the recorder retains the latest value rather than a confirmed departure event. The threshold is our definition, not a universal agency standard. Do not label this as audited actual departure OTP.

### Reported cancellations and notice

Count each cancelled trip/service date once, using its earliest feed timestamp for notice. The denominator is the exact active archived timetable, including routes without observed trips. The percentage is unavailable when there is neither an observed trip nor a cancellation in a chunk. This guard does not prove complete cancellation-feed coverage: unreported missed trips remain unknown.

`rate = 100 × Σcancelled_count / Σscheduled_count`

`short notice = 100 × Σno_notice_count / Σcancelled_count`

The 15-minute notice cutoff is editorial. Trips with any cancellation report count even if another feed also showed them operating; the measure is reported cancellations, not verified kilometres or trips lost. Trip counts use any retained delay observation and the trip's first-departure band. They are not the OTP denominator.

### Screened GPS passages

The regularity calculation uses `metric_passage`, reconstructed from `vehicle_position`, independently of the legacy visit detector. Each estimate links to its two source GPS row IDs. A segment must be at most 30 seconds long, imply at most 35 m/s, and pass within 35 m of the scheduled stop. STOPPED_AT or progression from that stop to its immediate scheduled successor must corroborate proximity. The measurement timestamp is used when retained; older records fall back to the feed timestamp.

Reject terminals, endpoints, repeated stops, separated visit episodes, multiple vehicles on a trip and nonmonotone stop progression. The spatial/time thresholds are screening choices, not a guaranteed error bound. Feed status is corroboration from the same source, not independent ground truth. Sparse GPS, incorrect assignments and upstream timestamp lag remain limitations.

### Matched wait and regularity windows

The unit is an interior timepoint × route × direction × service date × band. A candidate needs at least three scheduled passages. Every scheduled passage must have a screened observation or an explicit cancellation; otherwise the entire window is excluded. A valid comparison also needs at least three actual passages. No headway is removed solely because it is very short or very long.

Use the common interval bounded by the band, the first scheduled/observed passage and the last scheduled/observed passage. Integrate `next arrival − t` over this **same interval** for both timelines. For complete intervals this reduces to `Σh² / 2`; clipping handles boundaries exactly. This avoids assuming `scheduled wait = mean headway / 2` when the timetable is irregular.

For each route:

```
AWT minutes = Σwait_observed_area / Σwindow_seconds / 60
SWT minutes = Σwait_scheduled_area / Σwindow_seconds / 60
EWT minutes = AWT − SWT
CV = Σcv_weighted_sum / Σcv_weight
```

CV uses population standard deviation / mean for complete observed gaps ending inside a window, with at least two gaps. Compute this within the window, multiply by its duration, then sum. The system EWT and CV are equal-weight means of contributing route values. AWT pools exposure across sampled windows. These weights are explicit choices because no passenger counts are available; they do not reproduce TfL's network measure.

Selection remains consequential. Unknown passages exclude windows, including potentially disrupted ones. Observed/cancelled completeness cannot establish that every unscheduled bus was detected. The interval excludes service edges without a following observed arrival. Compare the coverage and contributing routes alongside each value.

## Industry interpretation and trends

[DfT's quality report](https://www.gov.uk/government/publications/buses-statistics-guidance/annual-bus-statistics-quality-report) distinguishes punctuality for non-frequent service from excess waiting for frequent service. Thunder Bay's less frequent routes should lead with punctuality and cancellations. EWT assumes random arrivals; it is a spacing diagnostic for timetable-aware passengers, not their expected personal wait.

[TfL's performance reporting](https://tfl.gov.uk/corporate/publications-and-reports/buses-performance-data) compares corresponding quarters across years to reduce seasonal confounding. With only April–August available for this audit, there is no seasonally matched annual comparison. Do not interpret monthly values as a seasonally adjusted improvement score.

The D3 trend sits below the route comparison and follows the selected metric card. It provides daily readings and a trailing 30-calendar-day aggregate (labelled “30-day average”), with the selected month shaded vertically. Hover, touch or focus the chart and use arrow keys to read individual dates. The route selector is the only trend filter; readings include all calendar days and all three time bands (06:00–24:00). Exact service availability comes from the archive calendar. The default is the previous completed month. History ends with the selected month, excluding the unfinished service day. The public page uses a simple days-of-observations summary; detailed coverage counts remain in the exported data and their interpretation is explained on the Method page.

When Cancellation Rate is selected, the cancelled-trips list below the trend follows the same route selector. Its desktop table, mobile cards and summary (including scheduled-trip totals) cover the selected route and month. “All routes” restores the full list; changing routes preserves the current sort order.

Rolling values recompute from raw counts/integrals. They require a full 30-day calendar span, at least 70% of selected calendar days with valid readings, and a reading on the final day. The 70% rule is a display threshold documented on the Method page, **not** a confidence interval or industry requirement. Real gaps break the line. No interpolation fills them. Samples and the set of routes may change even when this rule passes.

## Implementation

- `metric_schedule.go`: validates and archives local GTFS bundles. Content hashes retain provenance without overwriting older schedules. Calendar exceptions are applied. Among archives covering a date, prefer the one matching the most observed/cancelled trip IDs, then publication timing. A later capture can cover an earlier service date; this does not prove the timetable was identical when originally published.
- `metric_passage.go`: screened historical reconstruction; raw source tables remain unchanged.
- `metric_day.go`: reads each event source once for the date, matches the archived timetable, counts punctuality/cancellations and assembles windows.
- `recipes/window.go`: pure, tested wait integrals and window CV.
- `chunk.go`: one transaction replaces the day's rows and recipe marker.
- `chunk/chunk.go`, `chunk/rollup.go`, `static/transit/chunks.js`: additive data and matching server/browser reducers. Tests run the JavaScript reducer against Go.
- `chunk_rollup.go`: refreshes today, repairs missing/stale recent dates and finalizes yesterday. Invalidated older chunks are also repaired beyond the usual 60-day scan. It only rebuilds dates covered by an archived schedule.

Recipe version 0 rows remain stored but are excluded from corrected figures. New timetable imports invalidate affected rollup versions; rebuilding also reconstructs their passages. The old trip-average OTP and raw headway columns remain in the SQL table for compatibility and audit, but corrected reads no longer use them. Legacy and corrected versions must never be pooled.

[GTFS service times](https://gtfs.org/documentation/schedule/reference/) are measured from local noon minus twelve hours, including DST transitions. The recording service date changes at 04:00 local; public bands cover 06:00–24:00. The recorder now retains vehicle timestamps, trip service dates and stop sequences for future validation.

## Rebuild an archive

Apply migration 28 first. It preserves duplicate local visit records in `stop_visit_duplicate_archive` before installing the service-date key. The down migration deliberately refuses a lossy rollback; restoring a pre-migration backup is required to run the old recorder.

Build the helper, then import each historical GTFS bundle with its capture/commit time:

```sh
go build -o bin/transitmetrics ./cmd/transitmetrics
./bin/transitmetrics -schedule-dir /path/to/gtfs \
  -source <git-commit-or-archive-reference> -published-at 2026-07-02T12:00:00Z
./bin/transitmetrics -from 2026-04-12 -to 2026-08-31 -gps
```

`DATABASE_URL` selects the target. Timetable imports and derived-table rebuilds write to that database. Test against a restored local copy first. The helper does not refresh the live timetable or contact the transit feed. Re-running a date is idempotent. Archived raw GPS is required to recover lost visits; merely repairing the key cannot recover earlier suppressed events.

The normal GTFS refresh automatically archives new bundles. Startup also archives the installed bundle when the refresher has no newer download. Importing older history does not replace the application's live route/planner tables.

Exports now include counts, timepoint delays, passages with source IDs, rebuild provenance, archived timetables/calendar, cancellations and alerts. They reproduce the aggregate calculation, but exclude the large raw GPS source needed to independently rerun detection.

## Checks

```sh
go test ./...
TRANSIT_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  go test -race ./internal/transit ./internal/transit/chunk ./internal/transit/recipes
go vet ./...
npm run css
npx eslint static/transit/chunks.js static/transit/trends-chart.js
```

PostgreSQL tests create and remove scratch databases. Fixtures cover early/late cancellation in the old OTP formula, cancellation deduplication, missing-passage suppression, archived schedules after live tables change, visit identity across service days, finalization after midnight, variable schedules, signed EWT, extreme headways, DST and Go/JavaScript parity. Historical validation results are recorded in [transit metric audit](transit-metric-audit.md).
