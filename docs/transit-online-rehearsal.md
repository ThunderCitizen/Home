# Online transit deployment rehearsal — October 4, 2026

This rehearsal used a fresh restore of
`backups/thundercitizen-2026-09-10T18-04-08Z.sql.gz`, an isolated
`transit_backup_rehearsal_online_*` database, two read-only instances of the real
HTTP server, local Caddy containers, and the real DigitalOcean Space. Production
and the normal development app database were not changed.

## Database preparation and handoff

All **63,584,358 original raw rows** reconciled by table row counts and full-row
multiset fingerprints after conversion. Concurrent transactions performed actual
inserts, updates, and deletes during the copy; surviving inserts were checked
before removing only the synthetic rehearsal rows.

| Measurement | Result |
|---|---:|
| Backup restore | 423.49 s |
| Legacy service-date preparation | 23.26 s |
| Migration 28 metadata step | 1.078 s |
| Migration 29 metadata step | 0.044 s |
| Initial bulk copy and delta replay | 284.51 s |
| Successful concurrent write transactions during bulk copy | 5,392 |
| Bulk-copy write latency, p95 / p99 | 24.45 / 157.97 ms |
| Resumed analysis and delta replay | 30.64 s |
| Final database handoff, including final replay | **310 ms** |
| Resumed-run indexed reads, p99 / maximum | 1.65 / 41.38 ms |

Two lock failures were deliberately retained as recovery evidence. Maintenance
on the source table blocked trigger installation; maintenance on a private copy
blocked the first handoff. Both failed without replacing live tables or losing
rows. The implementation now uses non-queuing trigger-lock retries, disables
maintenance on private copies, analyzes them before handoff, and restores normal
maintenance atomically when they become live. The successful retry reused the
completed copies. Its 30.64 seconds is **not** the time to copy the full backup.

Preparation metadata changes also use transactional `NOWAIT` acquisition and
retry without leaving a dirty schema marker. A regression holds a live reader
open and verifies another reader remains unblocked. Further tests cover an
uncommitted lower journal sequence, cross-month updates, duplicate evidence,
unknown dependent views, complete rollback, and repeated preparation/cutover.
The exact shipped CLI also passed `prepare → prepare → cutover → cutover →
prepare` against a fresh disposable database, retaining eight bundled schedules.

## HTTP availability

A real Caddy reload moved traffic between healthy instances before stopping the
old instance: **631 requests, zero failures**. Restarting the proxy retained the
selected upstream. A separate monitor sent **13,548 requests, zero failures**
through the rehearsal proxy across online copying, failed handoff recovery,
final handoff, and fingerprint verification; maximum response time was 126 ms.
These are local measurements, not a production latency guarantee.

The **first adoption of the new Caddy startup command is a separate one-time
container recreation**. A real Compose rehearsal measured 0.494 seconds for
recreation and 13 failed probes spanning 0.330 seconds. This initial transition
is not zero-downtime. Subsequent app deploys use graceful reloads instead.

Deployment command tests cover unhealthy candidates, failed proxy verification
(restoring the previous route), and interrupted database handoffs. Failed final
handoffs keep the candidate serving read-only and persist the exact candidate
image/step for resumption. Writers activate only after the previous process has
stopped and schema cutover succeeds.

## Real bucket round trip

Destination:
`transit/rehearsals/multimonth-20261004T170142.619912000Z/`
in `thundercitizen.tor1.digitaloceanspaces.com`.

The production closer finalized July and August using the bundled historical
GTFS schedules, verified its real uploads by reading them back, published the
immutable ZIP releases, and removed only those months' raw partitions. September
remained live. Repeating the closer uploaded nothing.

The latest ZIP has two monthly rows and **1,156 route/day rows**: 582 for July,
574 for August, covering all 62 service days. Both CSVs matched the retained DB
summaries; July's earlier immutable release remained available. The running
Method page picked up the new download URL without restarting or changing envs.

[Download the rehearsal ZIP](https://thundercitizen.tor1.digitaloceanspaces.com/transit/rehearsals/multimonth-20261004T170142.619912000Z/reports/archive/2026-08/e9d2174cecd1de0022f56f1b6d0bfaaa01f5716f55da01b77e57ff76172a2e5d/transit_reports_v1.zip).

Anonymous raw downloads returned 403. All 36 August archive payloads plus their
manifest were downloaded with authentication (257,301,189 compressed bytes),
verified, and restored into an isolated analysis schema. Restored raw counts and
fingerprints matched the pre-close evidence, including 10,647,080 GPS records,
909,909 alerts, 767,353 stop delays, and 297,076 cancellations. No production
bucket objects were replaced or deleted. Rehearsal objects and harmless
`transit/rehearsals/online-private-probe-*` objects remain for operator cleanup.

Local logs, fingerprints, CSVs, and downloaded archive evidence are under
`/tmp/transit-online-rehearsal/`. The original backup remains unchanged. The temporary HTTP/proxy processes and
scratch database were removed after verification; local evidence and remote
rehearsal objects were retained.

## Repeating the tests

Restore the backup into a new isolated database whose name starts with
`transit_backup_rehearsal_online_`. Set `TRANSIT_ONLINE_REHEARSAL_DATABASE_URL`
and `TRANSIT_BACKUP_TEST_ARTIFACTS` to that database and a new artifact directory.
Run these sequentially:

```sh
go test ./internal/transitclosing -run '^TestOnlineBackupMigration$' -count=1 -v -timeout 90m
# Explicitly opt in to real bucket writes, with SECRETS_FILE pointing to the
# local key file and TRANSIT_ARCHIVE_BUCKET/ENDPOINT set for the rehearsal.
TRANSIT_SPACES_MULTIMONTH=1 go test ./internal/transitclosing -run '^TestOnlineBackupBucketLifecycle$' -count=1 -v -timeout 90m
```

The bucket test consumes the successful migration fingerprint checkpoint. It
slices only the disposable copy to July–September and preserves the chosen
remote prefix across retries. Set `TRANSIT_ONLINE_REHEARSAL_HTTP_URL` to a
read-only rehearsal app to additionally verify its live Method response.
