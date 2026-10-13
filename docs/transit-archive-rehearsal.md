# Real-backup archival rehearsal — October 4, 2026

**Passed.** Restored `backups/thundercitizen-2026-09-10T18-04-08Z.sql.gz`
into the isolated PostgreSQL 16/PostGIS database
`transit_backup_rehearsal_febdda9f0c`. The original backup and application
database were not modified. No DigitalOcean objects were written.

## Dataset and outcome

The backup contained **63,584,358 raw rows** at migration 22, including
53,523,644 GPS positions. The test upgraded through migration 31, comparing
original-column fingerprints across all upgrades and full-row fingerprints
immediately before/after partitioning. All comparisons passed.

After that full-dataset check, the disposable database was trimmed to
August–September. Eight saved GTFS versions supplied historical schedules.
The real passage reconstruction and metric finalizer processed all 31 August days.

| August data | Rows |
|---|---:|
| GPS positions | 10,647,080 |
| Alerts | 909,909 |
| Stop delays | 767,353 |
| Cancellations | 297,076 |
| Stop visits | 115,033 |
| Vehicle assignments | 28,421 |
| Reconstructed passages | 58,356 |
| Retained August daily route summaries | **574** |
| Retained August metric-band summaries | **1,657** |

Count clarification: the first run's whole-table checks recorded 759 daily and
2,212 band summaries, including retained September rows. August itself accounts
for 574 and 1,657 respectively, as confirmed by the archive manifest and CSV.
The original published August CSV correctly excluded September.

The 12,764,872 original August raw rows occupied approximately **2.7 GiB**
including indexes, before passage reconstruction. The completed compressed
archive occupies **257,306,426 bytes** across 36 tables plus its manifest;
this includes reference data and boundary evidence as well as month-owned rows.
The consolidated daily CSV is **46,446 bytes** and monthly CSV **273 bytes**
for this one-month slice. Reference schedules, provenance and the journal remain
in PostgreSQL in addition to the retained summaries.

## Checks exercised

- Full-backup row-count and content-fingerprint reconciliation before slicing.
- Independent August timestamp bounds: August 1 08:00 UTC through September 1
  08:00 UTC. September 1 before local 04:00 correctly contributed **23,869 GPS
  positions and 2,239 alerts** to August.
- No month closure at 03:59:59; closure permitted at 04:00 Eastern.
- Real timetable import, GPS passage reconstruction and daily/monthly rollups.
- Injected failure uploading the second public CSV: raw rows stayed intact,
  journal remained `frozen`, and no report release was published.
- Successful retry published both CSVs; each matched the frozen summary export
  byte for byte.
- A dependent view blocked partition removal after publication. Removal rolled
  back; dropping the blocker allowed completion without another upload.
- Every month-owned raw row was removed only after verified publication.
  September/outside-August raw fingerprints and retained summaries stayed equal.
- Restoring the uploaded archive into a separate analysis schema reproduced
  every owned table's row count and content fingerprint, including passages.
- Re-running the completed closer was a no-op.

Row fingerprints use count plus the sum of PostgreSQL's 64-bit row hashes;
they are regression checks, not cryptographic proof of row equality. Archive
files are separately verified with SHA-256, and report comparisons were exact.

## Recovery improvements from the rehearsal

1. **Checkpoint verified raw uploads before CSV publication.** A late CSV failure
   previously exported/uploaded the raw month again. The worker now durably saves
   the verified raw manifest and prefix first. PostgreSQL regression tests cover
   worker restart, unchanged private-upload count, prefix and verification time,
   early failures without a checkpoint, and incomplete checkpoint rejection.
2. **Provision historical partitions for every raw table.** Newly introduced
   derived tables may lack a partition for an older closing month. The worker
   now provisions the target month before finalization and can atomically move
   existing frozen/published fallback rows into it. Tests verify historical
   passage archival, raw/derived row fingerprints and restored write guards.
   Structural operations use a five-second lock timeout.

The long backup run started before these two fixes were compiled; it completed
the original lifecycle successfully. In particular, its pre-boundary check had
already provisioned August, so the dedicated historical catch-up test was needed
to cover the missing-partition case. The fixes passed the full PostgreSQL closing
and archive suites afterward, and `go test ./...` passed.

## Timing and reproducibility

- Full backup restore: **6m 55s**.
- Partition-copy migration 30: **4m 57s**.
- Complete backup test, including repeated full-table checks: **22m 42s**.
- August finalize/export/failure/retry/restore/check phase: **4m 22s**.

These are local-machine measurements, not production timing guarantees.
Migration 30 still needs a maintenance window and temporary disk headroom.

Run instructions are in [transit-archival.md](transit-archival.md#rehearse-against-an-actual-backup).
The opt-in test is `internal/transitclosing/backup_test.go`. Small JSON results,
the test log and the local object-store evidence are under
`/tmp/transit-backup-rehearsal/`. The disposable database was removed after all
checks to reclaim disk space; the original backup and evidence files remain.
The published raw manifest hash was
`9523bd0f26e8e62dd450776c557a3e8e6766f711de4d318f8a95ad49d0d2bfa2`.

The initial rehearsal used a filesystem object-store double with copy/read-back
SHA-256 checks. The subsequent live Spaces test below verified the real endpoint,
local credentials, object ACLs and network transfers.

## Live Spaces rehearsal — October 4, 2026

**Passed in 2m 22s**, using the existing local Spaces credentials and the same
backup-derived August archive/report files. The production upload implementation
uploaded 36 compressed tables, their manifest, and both public CSVs beneath:

```text
thundercitizen/transit/rehearsals/20261004T151636.538136000Z/
```

A harmless private probe first confirmed authenticated write/read access and
anonymous denial. All raw objects subsequently passed authenticated read-back
SHA-256 checks, a separate download, manifest verification, and anonymous HTTP
403 checks. Both public CSVs returned HTTP 200 and their exact expected bytes.

The downloaded evidence was restored into a new disposable PostgreSQL database.
All eight owned raw-table counts/fingerprints matched the original rehearsal,
including 10,647,080 GPS records and 58,356 reconstructed passages. The temporary
database was dropped automatically. The original backup, app database, existing
bucket objects and production report pointer were untouched.

**Remote files remain for operator cleanup:** delete only the timestamped prefix
above when finished inspecting it (40 objects including the private probe).
The public reports are in its `reports/` folder; private evidence is in `raw/`.
Downloaded files and the result JSON remain at
`/tmp/transit-backup-rehearsal/artifacts/spaces-20261004T151636.538136000Z/`;
the log is `/tmp/transit-backup-rehearsal/spaces-rehearsal.log`.

This verifies live storage and backup restore, using the completed archive from
the earlier month-closing test. It does not enable or execute the production
month closer. Repeatable instructions are in the
[Spaces rehearsal runbook](transit-archival.md#rehearse-with-real-spaces-credentials).

## Multi-month live download rehearsal — October 4, 2026

**Passed.** A fresh copy of the original backup was sliced to July–August:
26,053,589 raw records, including 21,913,909 GPS positions. Restore took 3m 30s;
schema upgrades, both real month closures/uploads and HTTP checks took 5m 52s.
The production worker closed July then August using the real Spaces store,
with all object paths redirected beneath this isolated prefix:

```text
transit/rehearsals/multimonth-20261004T152612.144427000Z/
```

- Both journal entries reached `complete`; the verified raw partitions were
  removed from the disposable database.
- The latest monthly CSV contains **2 rows**, July and August (356 bytes).
- The latest daily CSV contains **1,156 unique route/day rows** across all 62
  dates: **582 July + 574 August** (93,123 bytes).
- Anonymous CSV downloads matched the retained summary exports byte for byte
  and carried the CSV content type and attachment headers.
- All 574 August rows also exactly match the earlier single-month rehearsal CSV.
- July's original immutable monthly release still contains only July.
- The actual Method-page HTTP handler/template returned 200 and linked both
  downloads to the August release containing both months.
- Re-running the completed worker performed no additional uploads.

There are **78 objects** across the two raw archives and report releases, left
for operator inspection and cleanup. Production and existing releases were
untouched. The disposable DB was removed after checks; local CSVs, rendered
Method HTML, prefix and result JSON remain in
`/tmp/transit-multimonth-rehearsal/artifacts/`.

Latest reports:

- [Monthly CSV](https://thundercitizen.tor1.digitaloceanspaces.com/transit/rehearsals/multimonth-20261004T152612.144427000Z/reports/2026-08/d60183e3d0ade9b46302b768e079baa97632102c9bf9ecf4708b706f16d3e644/monthly_metrics.csv)
- [Daily route CSV](https://thundercitizen.tor1.digitaloceanspaces.com/transit/rehearsals/multimonth-20261004T152612.144427000Z/reports/2026-08/d60183e3d0ade9b46302b768e079baa97632102c9bf9ecf4708b706f16d3e644/route_day_summaries.csv)
