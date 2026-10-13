# Historical GTFS recovery snapshots

`historical.zip` and `manifest.json` are embedded in the application binary.
They preserve the eight saved timetables recovered into
`data/metric-validation/schedules/`, which is local-only and excluded from Docker.
The manifest records each original Git commit, capture time, and metric schedule
content ID. These files must be included in version control.

Each ZIP directory contains the original `trips.txt`, `stops.txt`,
`stop_times.txt`, `calendar.txt`, and `calendar_dates.txt` bytes used by
`ImportMetricSchedule`. Other GTFS files are unnecessary for historical metrics.
The content ID is SHA-256 over each filename plus newline and its bytes, in that
order. ZIP entry timestamps are fixed for reproducibility.

Startup imports missing IDs in capture order before starting rollups or archival.
It never replaces the live timetable. New upstream timetables continue to be
archived in PostgreSQL by the regular GTFS refresher; this bundle is for initial
historical recovery, not a manually maintained feed mirror.

`TestBundledHistoricalSchedules` checks every bundled content ID and provenance;
`TestHistoricalSchedulesImportAcrossRestarts` imports the actual snapshots into
a disposable database twice to verify recovery and idempotence.
