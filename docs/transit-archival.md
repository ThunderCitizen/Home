# Transit operating-month archival

The daily directory reads retained `transit.route_day_summary` rows: one route × service date with additive statistics and provenance. Metrics and time-of-day views retain `transit.route_band_chunk` rows: one route × service date × band, with additive inputs, coverage and metric versions. Both stay in production. Reference schedules and rebuild provenance remain in production. Raw event archival is independent of that presentation contract.

The server closes completed operating months automatically when a Spaces bucket
is configured. The operating calendar flips at **04:00 America/Thunder_Bay**, also
on the first of each month. There is no additional grace period: normal service
has already ended. `cmd/transitarchive` remains a separate non-destructive
snapshot/restore tool; its default 48-hour grace does not apply to the worker.

## Enable automatic closing

Create a **private DigitalOcean Space** with no public bucket-wide policy. The
worker gives only the consolidated ZIP public-read object ACLs. Raw objects
remain private. Supply:

- `TRANSIT_ARCHIVE_BUCKET`: bucket name.
- `TRANSIT_ARCHIVE_ENDPOINT`: regional endpoint, default `tor1.digitaloceanspaces.com`.
- `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`: bucket-scoped credentials with
  object write/read and ACL permissions. Object read is required for verification.

Production Compose accepts the bucket and endpoint in `.env` and reads credentials
from `secrets/transit_archive_access_key` and `secrets/transit_archive_secret_key`
(mode 0600). For a local server, environment variables or `secrets.conf` work.
Do not enable expiration of raw archives. Multipart-upload cleanup is fine.

Without a bucket, the server provisions partitions but does not seal or remove
history. Credentials/configuration errors disable closing and log the reason.
No bucket listing or remote upload runs on page requests.

### First deployment

Use `./scripts/deploy.sh` for production. It runs the new image's
`transitprepare -action prepare` **while the old app is serving**:

1. Backfill legacy stop-visit service dates in batches and build its replacement
   unique index concurrently. Retain the old writer's conflict target until handoff.
2. Apply the small metadata changes through migration 29, import bundled historic
   timetables, and create the empty report journal for the read-only candidate.
3. Build partitioned replacements in the private `transit_prepare` schema.
   Switch GPS and alerts individually after their copies catch up, releasing
   each original before copying later tables (see incremental conversion below).
   Short `NOWAIT` locks install transactional change capture; a busy table is
   retried without queuing a lock ahead of live requests. Each table is copied
   in a repeatable-read snapshot and checkpointed independently.
4. Replay committed inserts, updates and deletes. Changes already visible in the
   copy's snapshot are skipped; no sequence watermark can skip a transaction that
   commits later. Analyze the copies before handoff. Their autovacuum is disabled
   until they become live, preventing maintenance from extending the cutover.

The deployer checks the new HTTP instance's health and pages, switches Caddy to
it, and stops the old recorder. The candidate is read-only at this point.
`transitprepare -action cutover` drains remaining deltas before taking short DDL
locks, replaces the tables, installs migration 31's guards, restores normal
maintenance, and advances the schema version in one transaction. Unknown
foreign-key/view dependencies or lock timeouts roll the switch back. Only after
success does the deployer activate the new recorder and background rollups.

Preparation is resumable and needs temporary space for the largest individual
replacement or the remaining six-table group, plus WAL and working space. Keep a recent backup. Do not remove `transit_prepare` manually
while capture triggers are installed. A failed handoff leaves HTTP serving from
the read-only candidate; rerun the deployment to finish it. See
[deployment recovery and the one-time proxy transition](../DEPLOY.md).

The SQL-only migration 30 remains the offline/dev path and copies under locks;
do not use a bare server restart or direct migration command for this initial
production conversion. Production HTTP slots skip implicit startup migrations.
Local server startup still fails on a migration error or dirty marker rather
than running against a half-applied schema. No migration evicts historical
months; the verified monthly close worker performs that later.

Once enabled, the worker catches up oldest-first, including historical months.
No cron job, monthly CLI invocation, or report-URL environment update is needed.
The initial production setup still requires the two credential files and a
deployment of the migrations/app. A local rehearsal URL does not enable the
production worker and should not be copied into production configuration.
Each month must have archived GTFS coverage for its observed service dates.
Missing coverage leaves the month in `preparing` with an error and retains raw
data; import the historical GTFS and it retries automatically. Existing recipe
versions must be finalized before upgrading. A later recipe change requires
restoring archived evidence into analysis storage and deliberately rebuilding
historical reports; the worker will not overwrite those values from absent raw.

Fresh containers recover the current timetable even if its feed hash is
unchanged: the refresher extracts the downloaded bundle and ensures its metric
schedule exists. Eight saved historical timetables (captured April 12–August 28,
2026) ship in the binary under `internal/transit/schedules/`. On startup they
are imported automatically before rollups or month closing start. HTTP and raw
recording continue during import; failures retry every five minutes. Content IDs
make restarts idempotent, and original capture times preserve schedule selection.
History outside these saved schedules can still require another timetable import.

The full online migration, HTTP handoff, and real bucket round trip are recorded
in [the October online rehearsal](transit-online-rehearsal.md).

### Lifecycle and recovery

`transit.archive_month` is the durable journal:

1. `preparing`: check timetable coverage.
2. `sealed`: wait for active recorder transactions to drain, then reject stale
   feed echoes for that month. Reconstruct passages and roll up every scheduled
   or observed day. No separate late-update ledger is maintained.
3. `frozen`: acquire the exclusive month lock after rebuilds finish. Raw data,
   derived passages and retained summaries can no longer change.
4. `uploaded`: a consistent raw snapshot and the report ZIP have been uploaded
   and read back with matching SHA-256 hashes and sizes. The raw manifest also
   records row counts, column types, schema migration version and predicates.
5. `published`: commit the new report release pointer. The Method-page download
   now refers to the new immutable ZIP; no environment edit is needed.
6. `complete`: verify the manifest covers every month's partition and row count,
   then drop the raw partitions and mark completion in **one transaction**.

The worker runs at startup, wakes at 04:00 boundaries, and retries/catches up
at most five minutes later after errors. A cross-process advisory lock prevents
two app instances closing simultaneously. Upload failures leave the month frozen
and its data intact. Once all raw files and the manifest verify, their location
and verification timestamp are checkpointed before report upload. A report
failure retries only the small ZIP, reusing the verified raw archive. Failures
before that checkpoint can leave orphan immutable prefixes, but never publish
incomplete releases. A crash after publication resumes eviction without
re-uploading. Partition-removal failures roll back all removals. Empty operating months close without inventing metric rows; an entirely empty report contains only column headers.

```sql
SELECT month, state, attempts, updated_at, last_error,
       raw_prefix, raw_verified_at, report_base_url
FROM transit.archive_month ORDER BY month;
```

`raw_prefix` identifies the private HTTPS object prefix. Download that complete
prefix with authenticated S3 tooling for restore. The manifest is saved in the
journal too. Files use `transit/raw/YYYY-MM/<manifest-hash>/`; public reports
use `transit/reports/archive/YYYY-MM/<manifest-hash>/`. Temporary compressed export files
need local disk space for one month and are removed when a run finishes. The
archive format remains compatible with the restore tool below.

The Method page resolves the latest published journal entry on each request and
uses its recorded ZIP filename, so a recipe-version upgrade does not prematurely
change the link to an artifact that has not been published yet. The explicit
fallback URL is used only before any automatic publication exists.

Automation regressions exercise 13 consecutive historical months with a new
worker each cycle, a missed monthly wakeup, idempotent retries, a rolling
12-month ZIP, all 13 DB summaries retained, unchanged older archive ZIPs, and
the recorded artifact version. Separate tests cover local 04:00 wakes across
both DST transitions and missing timetable files after container restarts.

### Retained data and routing

Daily route summaries, band summaries, archived timetables, rebuild provenance
and the close journal stay in PostgreSQL. The Monthly page no longer requests
individual cancellation events. Historical timetable/audit detail requests return
an explicit archived response linking to Daily and Method, rather than implying
zero observations. Rebuilds and timetable invalidation cannot mutate frozen
months.

GPS/alerts use timestamp partitions bounded at 04:00 Eastern; stop delays,
assignments, passages and stop visits use their operating date. Cancellations use
the trip's service date, so an October cancellation announced in September stays
until October closes. The legacy duplicate-stop archive follows service dates.
Small feed-gap diagnostics and reference tables remain in the database.

Current and next-month partitions are provisioned automatically. Fallback
partitions prevent feed outages if provisioning is delayed. Rows are moved into
new partitions transactionally. Historical closing also provisions every raw
table for its target month, including derived tables introduced after that month.
Recovery can structurally relocate frozen/published fallback rows without
passing through guards that reject logical edits; attachment restores the guards.
Provisioning and eviction use five-second lock timeouts so busy readers defer
the operation instead of indefinitely queuing recorder writes.
Invalid/unassignable legacy cancellation dates
remain in fallback for inspection; a close refuses eviction if fallback contains
rows owned by that month. Stale writes to already closed months are discarded by
the guard even after the original partition has been removed.

## Export and verify

```bash
# DATABASE_URL must explicitly identify the source database.
go run ./cmd/transitarchive -action export -month 2026-09 -dir /tmp/transit-2026-09

go run ./cmd/transitarchive -action verify -dir /tmp/transit-2026-09
```

The output directory must not exist. Failed exports leave a directory without a completed manifest; use a new directory for retry. The default grace period is 48 elapsed hours after 04:00 local time on the first of the next month. `-grace` changes this delay; negative durations and unfinished months are rejected. This only establishes snapshot eligibility, not that every metric has been finalized.

A read-only repeatable-read transaction captures every top-level table in the `transit` and `gtfs` schemas. Partition children are not exported separately. Each table becomes compressed PostgreSQL CSV with all columns, including original source IDs, nullable values, generated values, JSON, arrays and GPS coordinates. A manifest records format version, migration version, PostgreSQL version, column names/types, selection predicates, compressed SHA-256 hashes and row counts.

Selection rules:

- Stop delays, assignments, metric passages, chunks, daily route summaries and rebuild records use their service date within the selected month.
- GPS, stop visits (including the duplicate archive) and alerts include a one-calendar-day overlap on both edges. This retains overnight service and interpolation evidence. Monthly snapshots therefore overlap intentionally.
- Cancellations include either a matching trip start date or a feed timestamp in the overlap window, preserving late/early reports.
- Feed gaps include any interval overlapping the extended window.
- All other tables, including timetable versions, timetable services/trips, live reference metadata and GTFS, are copied in full. A new table is retained in full until its appropriate filter is explicitly defined, rather than silently omitted.

Verification streams gzip and CSV, checks column widths, counts logical CSV records (including multiline values), and verifies compressed hashes. No uncompressed event set is loaded into memory. A long-running snapshot may delay PostgreSQL vacuum cleanup; run exports off peak or on a suitable replica, and monitor disk headroom. Reference copies describe the snapshot time; the immutable metric timetable versions supply historical schedule provenance.

## Private Spaces upload

```bash
# Uses AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY through the existing config loader.
go run ./cmd/transitarchive -action upload -dir /tmp/transit-2026-09 \
  -bucket YOUR_PRIVATE_ARCHIVE_BUCKET
```

The default endpoint is `tor1.digitaloceanspaces.com`; `-endpoint` and `-prefix` are configurable. Use a dedicated private bucket: object ACLs are explicitly private, but a public bucket policy can still grant access. Objects are placed under `transit-archives/YYYY-MM/<manifest-sha256>/`. Every uploaded object is read back and hashed, and the manifest is uploaded last. There is no public URL, mutable latest pointer, deletion or lifecycle configuration. Retain these objects indefinitely unless an operator deliberately defines a different archive policy.

The CLI uploads but does not download. Use an S3-compatible client to copy the complete object prefix to a local directory, then run `verify` before restore. Checksum verification detects corruption; it is not a signature or proof against replacement by an actor who can modify both data and manifest.

## Restore for analysis

```bash
# Point DATABASE_URL at an analysis database migrated to the manifest's exact version.
go run ./cmd/transitarchive -action restore -dir /tmp/transit-2026-09 \
  -schema transit_archive_202609
```

Restore verifies the archive first, requires the recorded migration version and identical column names/types, then creates a new schema in one transaction. It refuses existing schemas and names outside `transit_archive_*`. Application tables remain untouched. For example:

```sql
SELECT count(*) FROM transit_archive_202609.transit__vehicle_position;
SELECT * FROM transit_archive_202609.transit__metric_rebuild;
```

Tables are named `<original-schema>__<original-table>`. Their column types and values survive, but indexes, foreign keys, identity generators and generated expressions are intentionally absent from these analysis copies. All generated values and original identity IDs are restored as ordinary values. Build analysis-specific indexes after restore. The archive is a logical dataset with schema metadata, not a standalone `pg_dump` or a disaster-recovery replacement. Keep the application revision/migrations that correspond to its recorded migration version. Re-running the existing metric pipeline requires a separate analysis database with canonical tables populated from these copies; this is not automated yet. Do not point the running production application at restored analysis tables.

## Validation

```bash
go test ./internal/transitarchive ./internal/transitclosing ./cmd/transitarchive

# Optional integration test: creates and drops only a uniquely named disposable DB.
# The supplied local PostgreSQL role needs CREATE DATABASE permission.
TRANSIT_ARCHIVE_TEST_DATABASE_URL='postgres://…/postgres' \
  go test -race ./internal/transitarchive ./internal/transitclosing -count=1
```

Tests cover grace/DST boundaries, multiline CSV, corrupted/truncated data, row counts, unsafe manifest paths and round-trip preservation of source IDs, timestamps, JSON, arrays and generated values. The integration test also checks that restore refuses an existing schema.

## Public consolidated reports

The Method page links directly to one prebuilt ZIP containing two CSVs, separate from the
private raw archive. There is no request-time database export. The old download
endpoint redirects to `/transit/method#downloads`.

Build the ZIP from a consistent snapshot of `route_day_summary`:

```sh
go run ./cmd/transitreports -dir /tmp/transit-reports-2026-10
```

`DATABASE_URL` is required. The current service month is excluded, using the
4 a.m. Eastern boundary. Included rows must be finalized and at the current
recipe version; repair stale summaries before publishing. Missing days are not
invented: monthly rows include counts of represented days and routes. Blank
metric cells mean unavailable. Percentages are in percent units; waits are
minutes and CV is dimensionless. Monthly metrics use the site's shared reducers,
including equal-route weighting for EWT and CV, not averages of daily percentages.

To export and publish to DigitalOcean Spaces:

```sh
go run ./cmd/transitreports -dir /tmp/transit-reports-release -bucket PUBLIC_REPORT_BUCKET
```

Uses `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` (including the existing
secret-file support). Default endpoint is `tor1.digitaloceanspaces.com`; override
with `-endpoint`. The public `transit_reports_v1.zip` contains:

- `monthly_metrics_v1.csv`: one system-wide row per completed month, with public
  metrics and counts of represented days/routes.
- `route_day_summaries_v1.csv`: Date, Route, Route name, Trips, Cancelled and
  OTP (%), matching the Daily cards' values and rounding. Unavailable values
  remain blank.

Both CSVs include the trailing 12 completed months through the release month.
For example, an October 1 rollup covers October of the previous year through
September. Earlier database summaries remain untouched. Missing months are not
invented; fewer than 12 months are included when less history is available.
The metric recipe version lives in the filename rather than a repeated column.
These CSVs are public presentation exports; full additive inputs, coverage
statistics, provenance and versions remain unchanged in the database. Route
labels use the same reference metadata as the UI. The filename helpers in
`internal/transit/reports.go` keep the worker, CLI and Method-page links aligned.

Every rollup regenerates the ZIP and stores an immutable copy under
`transit/reports/archive/<month>/<release>/`. Only after read-back verification
does the journal switch the current download. Older ZIPs stay in the archive,
so no bucket rename/delete or potentially stale mutable object is needed.

Manual publication uses a timestamped prefix under `transit/reports/archive/`; `-prefix`
can select an isolated rehearsal directory. Uploads are read back and verified
before the release URL is printed. After the ZIP upload verifies,
the printed `TRANSIT_REPORT_BASE_URL` can be used as a manual fallback until the first automatic publication. Automatic closing records its release in the database and takes precedence. Until either exists, Method says reports are awaiting publication.
Keep raw archives private; never set the raw archive bucket public.

## Rehearse against an actual backup

The [October 4 rehearsal results](transit-archive-rehearsal.md) record the
full-backup partition check, August closure/restore, timings and recovery fixes.

`TestBackupMonthLifecycle` is an opt-in destructive test **only for an isolated
restored copy**. It rejects database names without the
`transit_backup_rehearsal_` prefix before making any changes. Never rename the
application database to satisfy this guard. Restore a backup into a new database
using a matching PostgreSQL client, preserving the original backup file. Strip
source ownership/grants or use `pg_restore --no-owner --no-acl` for custom dumps.

The current real-data scenario uses the September 10, 2026 full backup and the
recovered GTFS directories listed in `data/metric-validation/schedules.json`.
Those large local fixtures are not committed. It:

- fingerprints original raw columns and compares them through all upgrades;
- checks complete raw tables before and after monthly partition conversion;
- independently checks August timestamp ownership against 08:00 UTC boundaries;
- slices only the disposable copy to August–September and imports saved GTFS;
- runs the real finalizer, interrupting publication during the public ZIP upload;
- blocks eviction with a temporary view, then verifies an upload-free retry;
- checks adjacent-month raw data and retained summary fingerprints;
- compares published CSV bytes with the frozen summary snapshot;
- restores the uploaded evidence and compares row counts and fingerprints.

```sh
TRANSIT_BACKUP_TEST_DATABASE_URL='postgres://…/transit_backup_rehearsal_UNIQUE' \
TRANSIT_BACKUP_TEST_ARTIFACTS='/tmp/transit-backup-rehearsal/artifacts' \
  go test ./internal/transitclosing -run '^TestBackupMonthLifecycle$' \
  -count=1 -v -timeout=90m
```

Uploads use a local filesystem test store, not Spaces. JSON reconciliation
artifacts and compressed evidence remain at the chosen artifact path. The test
leaves its explicitly supplied database available for inspection; remove that
isolated database afterward. Fingerprints combine counts and summed 64-bit row
hashes as a regression check; archived files additionally carry SHA-256 checksums.
This test scans the full restored dataset several times and performs a real
partition-copy migration, so allow substantial runtime and disk headroom.

### Rehearse with real Spaces credentials

`TestSpacesBackupRehearsal` uploads the previously verified backup archive and
its two report CSVs using the production `Spaces.UploadVerified` implementation.
It always writes beneath a unique `transit/rehearsals/<timestamp>/` prefix in
`thundercitizen`, leaving all remote objects for operator inspection/cleanup.
It never changes the production report pointer or deletes remote objects.

```sh
TRANSIT_SPACES_REHEARSAL=1 \
TRANSIT_ARCHIVE_BUCKET=thundercitizen \
TRANSIT_ARCHIVE_ENDPOINT=tor1.digitaloceanspaces.com \
SECRETS_FILE="$PWD/secrets.conf" \
TRANSIT_ARCHIVE_TEST_DATABASE_URL='postgres://…/postgres' \
TRANSIT_BACKUP_TEST_ARTIFACTS='/tmp/transit-backup-rehearsal/artifacts' \
TRANSIT_SPACES_REHEARSAL_SOURCE='/path/to/verified/raw/archive' \
TRANSIT_SPACES_REHEARSAL_REPORTS='/path/to/matching/report/directory' \
  go test ./internal/transitclosing -run '^TestSpacesBackupRehearsal$' \
  -count=1 -v -timeout=45m
```

The source directories are the successful release from the filesystem rehearsal;
the artifact root supplies its expected report hashes and raw-row fingerprints.
Credentials come from the existing secret loader; do not put their values in
the command. The test first checks anonymous denial of a harmless private probe,
then uploads all raw files privately, downloads/verifies them, checks every raw
object denies anonymous reads, and verifies both CSVs are anonymously readable
with exactly the expected bytes. It restores the downloaded archive into a new
disposable database and compares the owned raw-table fingerprints. The local
database is dropped automatically; downloaded evidence and JSON results remain.

### Multi-month live download rehearsal

`TestSpacesMultiMonthRehearsal` uses a fresh isolated backup database containing
July–August 2026 raw records and the saved GTFS versions. It upgrades the schema,
runs the real monthly worker against Spaces with all object keys redirected
under `transit/rehearsals/multimonth-<timestamp>/`, then checks the consolidated
downloads and the real Method-page HTTP handler/template.

```sh
TRANSIT_SPACES_MULTIMONTH=1 \
TRANSIT_ARCHIVE_BUCKET=thundercitizen \
SECRETS_FILE="$PWD/secrets.conf" \
TRANSIT_BACKUP_TEST_DATABASE_URL='postgres://…/transit_backup_rehearsal_UNIQUE' \
TRANSIT_BACKUP_TEST_ARTIFACTS='/tmp/transit-multimonth-rehearsal/artifacts' \
  go test ./internal/transitclosing -run '^TestSpacesMultiMonthRehearsal$' \
  -count=1 -v -timeout=90m
```

The latest report must contain both monthly rows and daily summaries spanning
all 62 service dates, with no duplicate route/date pairs. Its bytes must equal
the retained DB summaries; the prior July-only release must still be readable
and unchanged. The Method page must link the ZIP download to the latest published
release, and a completed-worker retry must perform no uploads. Remote releases
and local evidence remain for inspection; remove the supplied disposable DB
afterward. Never point this opt-in test at the application database.

## Incremental first conversion (October 2026)

The production preparation CLI now uses `PrepareIncremental`. It copies and
switches `vehicle_position` first, dropping the original heap in that same
transaction; then it does the same for `alert`. The old recorder's inserts and
alert conflict key are compatible with these partitioned replacements. Source
locks use `NOWAIT`; active readers/writers cause a retry rather than a waiting
DDL queue. Each switch checkpoints `transit_prepare.state.swapped` atomically.
An interruption can leave a mix of partitioned and original tables: rerun the
same deployment script to resume, and preserve the preparation schema.

The other six tables keep the coordinated final handoff, because some legacy
conflict keys change. Preparation does not evict historical records or depend
on bucket availability. Summarization, verified upload and historical partition
removal still happen after app activation.

This avoids retaining copies of all eight tables simultaneously. It still
requires a complete replacement of the largest table, plus indexes, WAL and
working space; it is not a bounded-size, month-by-month migration. Simply
committing smaller INSERT batches would not release the old table's disk space.
Use the measured rehearsal storage peak and production table sizes to assess
headroom, rather than assuming the former twice-total-data allowance is a hard
minimum. The full-backup online test now writes `disk-usage.json`, sampling
physical database size, allocated WAL and PostgreSQL temporary files once per
second during copy/handoff. A final tail of at most 100 changes per large table
is replayed under its switch lock, so a continuously writing feed cannot starve
the handoff. Retrying an unfinished copy first truncates its staging table in a
separate transaction to reclaim storage left by an aborted bulk insert.

Measured storage, interruption recovery and the full-backup verification are in
[the incremental preparation rehearsal](transit-incremental-rehearsal.md).
