# Transit metric audit — April–August 2026

Validated on September 27, 2026. The audit covers the recording, timetable,
reconstruction, aggregation, chart and export paths for transit metrics.
September observations are excluded from the historical conclusions below.
This is an audit of this application's calculations, not an independent audit
of Thunder Bay Transit's operations.

## Result

Rebuilt **142 service dates**, April 12 through August 31, from retained local
GPS and trip-update records plus eight GTFS versions recovered from Git.
The corrected detector retained **259,143 interior timepoint passages** with
source GPS row IDs. There are **18,178 usable regularity windows out of 29,214
candidates**, spanning **139 days** and 20 routes across the period.

The eight August dates with no headways in the old chunks now have usable
regularity windows:

| Date | Usable windows | Candidate windows | Contributing routes |
| --- | ---: | ---: | ---: |
| August 4 | 159 | 216 | 19 |
| August 6 | 154 | 216 | 20 |
| August 7 | 145 | 216 | 19 |
| August 10 | 156 | 216 | 20 |
| August 11 | 149 | 216 | 20 |
| August 14 | 138 | 216 | 20 |
| August 16 | 122 | 170 | 14 |
| August 17 | 156 | 216 | 19 |

Three gaps are retained: **June 25–27**. These are timetable mismatches, not
an absence of raw records. There are 960, 936 and 1,016 distinct trip IDs in
the retained daily delay records, but only 72, 83 and 80 match the selected
date-applicable archive. On June 25, searching all eight archived versions
also matches only those 72 IDs. Assigning the remaining records a timetable
by guesswork would create unjustified denominators and headways. These days
still have a small punctuality sample; they must not be interpreted as full
network coverage.

## Monthly results

All routes, all calendar day types, scheduled bands 06:00–24:00. April is
partial. EWT and CV first combine windows within routes, then average the
contributing route values equally. These are sampled service measures,
without passenger weighting.

| Month | Days with regularity readings | Screened passages | Usable/candidate windows | EWT, minutes | Headway CV |
| --- | ---: | ---: | ---: | ---: | ---: |
| April 12–30 | 19 | 33,890 | 2,121 / 3,830 | 2.153 | 0.179 |
| May | 31 | 56,698 | 3,895 / 6,328 | 2.402 | 0.177 |
| June | 27 | 50,643 | 3,632 / 6,276 | 2.977 | 0.192 |
| July | 31 | 59,556 | 4,182 / 6,422 | 1.583 | 0.163 |
| August | 31 | 58,356 | 4,348 / 6,358 | 2.141 | 0.177 |

Passages include recovered interior points that do not all fall within the
public bands or qualify for complete comparison windows. August's banded
coverage is 57,952 observed of 65,800 scheduled interior timepoints.

The old chunks had 337 / 675 / 582 / 316 / 454 populated route-day-band
headway rows in April through August. The corrected chunks have
769 / 1,386 / 1,248 / 1,443 / 1,432 rows with usable wait exposure. These
counts demonstrate recovered coverage; **the old and new KPI values are not
a like-for-like performance comparison**, because the populations and
formulas changed.

August reported punctuality is 89,632 / 93,935 samples (95.42%). The matched
timepoint records contain no negative preferred delay values, although the
raw stop-delay table does contain negative values elsewhere. There is no
global negative-delay clamp in our recorder. This does not establish that
buses never departed early: the feed may describe predictions or holding at
timing points. The page explicitly calls this feed-reported punctuality,
not verified actual departure OTP.

## Defects repaired

1. The checked-in visit key omitted service date, so recurring trip IDs
   conflicted with previous days. The local experimental schema instead
   used a surrogate key without the recorder's expected unique constraint.
   Migration 28 installs `(service_date, trip_id, stop_id)` and preserves
   duplicate local records separately.
2. Visit detection used route-wide stops, could interpolate across outages,
   and did not retry an initial reference-cache failure. The recorder now
   uses trip stops, a bounded interval, refreshed caches and exact entry
   timestamps for exit updates.
3. Historical calculations joined replaceable current GTFS tables. Archived
   timetables now preserve calendars, per-trip ordered stops, content hashes
   and capture provenance.
4. Trip-average OTP could cancel early and late departures against each
   other. Each timepoint now contributes its own early/on-time/late count;
   departure delay has precedence over arrival delay.
5. Headway thresholds discarded bunching and very long disruptions.
   Reconstruction now screens GPS evidence without truncating headways.
6. Scheduled wait assumed uniform spacing; EWT could be clamped; CV pooled
   incompatible stops and periods. Matched wait integrals and within-window
   CV replace those formulas.
7. A partially built date could remain permanently unfinished. Whole-date
   transactions and versioned markers now trigger finalization after 04:00
   local, independently of the PostgreSQL session timezone.
8. Advance cancellation reports could be lost at date boundaries or have
   their notice calculated by subtracting clock strings. Both summaries and
   details use the service date and earliest full timestamp.
9. Missing chart values became zeros. Nulls now remain gaps, including in
   the embedded JSON data. The 30-day line uses calendar days, raw aggregates
   and an explicit 70% completeness rule.
10. The separate diagnostic stats API used a conflicting ±60-second window,
    counted null samples and accepted future timestamps. It now uses valid
    samples, departure precedence and the same -60/+300-second thresholds,
    returns null for missing snapshot values, and identifies its all-stop
    population explicitly. Route schedule cancellation flags also follow
    service dates, including reports received before or after that date.

See [metric definitions and rebuilding](transit-metrics.md) for equations,
screening thresholds, weighting choices and source references.

## Local validation and application

The rebuild ran first in `citizen_metrics_audit`, with raw event tables read
through PostgreSQL foreign tables from the development dump. Each of the
142 dates completed. Repeating August 21 produced the same 1,931 screened
passages and 57 chunks.

Recovered GTFS revisions:

```
a8aee894e37542a72b56ec0b7cdb7e3c985a817a
3972e1773152281e9612a9470d558b92a6965e07
e83507ab3b844d3f542f8bdb330803cf481445ce
53ae3998b42c244580a958f7f475f526908dbcb0
a2d769447e273258ea0c0cb17b5d952d4b05b5ab
c48171238831a8d9852db06924db9487986c098d
107b41b17f5b12235fba070eb00965f6de6ea8d8
c16c422bb42e86d77b21a19dd2f6e00917a77f09
```

Before applying the local repair, saved
`backups/dev-before-metric-v1-20260927.dump`, containing the visit table,
original chunks and migration metadata. The local database had experimental
version 27 with no corresponding migration file. Migration 28 and its
version update were therefore applied in one checked transaction. A normal
checkout at migration 22 can apply migration 28 through the standard runner.

The migration preserved 2,976 duplicate visit records in
`stop_visit_duplicate_archive`. A subsequent transaction installed the
archived timetables, passages, rebuild markers and 7,654 corrected
April–August chunks in the local development database. The running local
development server continues recording and rebuilding current service.
The production snapshot and production server were not modified.

Validation includes the full Go suite with race detection, PostgreSQL
fixtures, `go vet`, JavaScript linting, server/browser reducer parity,
template/CSS generation, and desktop/mobile browser checks. Browser checks
found no JavaScript exceptions or horizontal page overflow. The August
export is a valid ZIP with 1,657 chunks, 93,935 timepoint samples, 58,356
screened passages, 31 rebuild markers and their timetable/calendar data.
Normalizing archived stop metadata once reduced this local month's export
from 55.0 to 9.5 seconds with byte-identical CSV contents. All export files
share one repeatable-read database snapshot.

## What remains uncertain

- A retained trip-update value is not a confirmed departure measurement.
- Historical GPS often has only a feed timestamp. Stop status and position
  come from the same feed, so corroboration is not independent validation.
- Excluding incomplete windows can preferentially exclude disrupted service.
  Coverage is shown alongside the result; no numerical confidence interval
  is claimed.
- The archive-selection rule maximizes matching active trip IDs. A later
  captured timetable covering an earlier date is traceable, but not proof
  that every stop time was identical on that date.
- Reported cancellations can coexist with observations for the same trip;
  unreported missed trips remain unknown.
- Five months cannot establish a seasonally adjusted annual trend. Compare
  the same route, day type and band; weekdays include holidays. Longer-term
  year-over-year comparisons need another year of comparable observations.

An additional GTFS archive covering the unmatched June 25–27 trip IDs would
allow those days to be investigated and rebuilt without inventing service.
