# Incremental preparation rehearsal — October 13, 2026

This changes the first production conversion to release large originals as it
progresses. `cmd/transitprepare` now uses `PrepareIncremental`: copy GPS, catch up
changes, atomically switch GPS and release its original; repeat for alerts;
then prepare the six remaining tables for the existing coordinated handoff.
Only the two tables whose legacy INSERT/conflict contracts remain compatible
switch while the old recorder runs. Archival and bucket verification are unchanged.

## Measured storage

A fresh isolated restore of
`backups/thundercitizen-2026-09-10T18-04-08Z.sql.gz` contained about 12 GiB of
PostgreSQL data, including about 9 GiB of GPS data and indexes. Production was
reported as about 16 GiB, so these are rehearsal measurements, not production
capacity readings.

During the full copy, samples recorded physical database files, allocated WAL
and PostgreSQL temporary files once per second:

| Measurement | GiB |
|---|---:|
| Minimum footprint after unfinished-copy cleanup | 12.76 |
| Peak footprint, immediately before the GPS switch | 23.56 |
| Additional sampled space | **10.80** |

WAL peaked at about 1 GiB; no PostgreSQL temporary files were observed during
this copy. GPS's original storage was released before copying alerts. Database
usage fell from approximately 23 GiB to 14 GiB at that switch. Preparation still
needs one complete replacement of the largest table; it does not bound storage
to one day or month. Sampling can miss brief peaks, and retained WAL, backups,
other workloads and production table/index proportions can change requirements.

For the reported 16 GiB production dataset, a planning allowance around
**20–22 GiB additional free space** includes margin above scaling this measured
copy peak. The reported approximately 27 GiB free looks workable for this path;
recheck after pulling the image, and keep the fresh backup off-server. This
supersedes the earlier unmeasured twice-total-data allowance for the old path.

## Recovery issues exercised

- Continuous writes prevented an empty-journal-only switch from progressing.
  The switch now replays at most 100 final changes for its own table while
  holding the short source lock; larger backlogs replay outside that lock.
- Vacuum on already-switched leaves blocked the final schema step. Its
  transaction rolled back cleanly. Maintenance now stays disabled on all
  replacement leaves until the final transaction restores it. Resume also
  reconciles leaves switched by the earlier implementation.
- An unfinished copy can retain physical pages after rollback. Retry now
  commits a staging-table truncation before recopying, reclaiming those files.
- Unknown dependent views roll back the affected table switch. A completed GPS
  switch survives a later alert failure; resume skips GPS and preserves writes
  made after its switch.

The bulk copy and replay took 488 seconds in this local run, including 9,551
successful concurrent insert/update/delete transactions. Local timings are not
production guarantees. After the maintenance fix, preparation resumed without
recopying. It spent 203.68 seconds reconciling existing maintenance and preparing
the handoff; that is not a full-copy timing. The final transaction took
**412.9 ms**. All **63,584,358 original raw rows** matched their before-copy
counts and full-row multiset fingerprints across all eight tables. All 2,027
surviving concurrent rows from that resumed run were present. Its 4,081 reads
and 4,055 write transactions completed without errors.

The full Go suite and PostgreSQL migration tests with the race detector passed.
A separate regression keeps reads and writes active until the early table switch
succeeds and checks every returned insert ID; it passed three repeated runs.
Unknown dependencies, late commits, interrupted copies, staged resume, a bounded
final delta batch and restoration of automatic maintenance are also covered.

Fingerprint checks use row counts and sums of PostgreSQL row hashes; they are
regression checks, not cryptographic proof. The existing archival SHA-256 and
bucket restore workflow is unchanged and was not rerun for this migration-only
change.

Artifacts are under `/tmp/transit-incremental-rehearsal/`:
`bulk-copy-disk-usage.json` and `bulk-copy.log` retain the measured copy attempt;
`resume.log` records recovery and final row verification. The original backup is
unchanged. No production database or bucket objects were changed in this run.

The disposable PostgreSQL container and its data volume were removed after
verification. Logs and measurement files remain in the artifact directory.
