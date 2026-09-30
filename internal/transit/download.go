package transit

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"thundercitizen/internal/cache"
	"thundercitizen/internal/logger"
	"thundercitizen/internal/transit/chunk"
)

var downloadLog = logger.New("download")

// avgBundleBytesPerDay is the per-day size of the compressed export ZIP, used
// only for the "estimated size" shown in the download dialog. Calibrated against
// the corrected August 2026 export (5.76 MB / 31 days). Timetable metadata adds
// fixed overhead, so short exports can differ from this rough daily estimate.
const avgBundleBytesPerDay = 192 * 1024

// EstimateBundleSize returns a human-readable, approximate size for the export
// ZIP over the given range (e.g. "~1.5 MB"). It never touches the database.
func EstimateBundleSize(dr DateRange) string {
	days := 1
	if from, err := time.ParseInLocation("2006-01-02", dr.From, TZ); err == nil {
		if to, err := time.ParseInLocation("2006-01-02", dr.To, TZ); err == nil {
			if d := int(to.Sub(from).Hours()/24) + 1; d > days {
				days = d
			}
		}
	}
	return humanBytes(int64(days) * avgBundleBytesPerDay)
}

func humanBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("~%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("~%d KB", b/(1<<10))
	default:
		return fmt.Sprintf("~%d B", b)
	}
}

// bundleFile is one CSV inside the downloadable ZIP: the filename it gets in
// the archive and the SELECT that produces it (date bounds already inlined).
type bundleFile struct {
	name string
	sql  string
}

// bundleFiles returns the CSV set for a date range. Together they are the
// minimum reproducible layer behind the Metrics tab: the aggregates plus the
// raw events they're computed from, so an analyst can recompute every KPI — or
// redefine one (e.g. their own "on time" window) — in a spreadsheet.
//
// dr.From/dr.To are re-formatted to YYYY-MM-DD by parseDateRange, so they're
// safe to inline. CopyTo takes a raw SQL string with no parameters.
//
// The large raw GPS log is excluded; passage source IDs support a separate
// detector audit. The legacy stop_visit table is not used by these recipes.
func bundleFiles(dr DateRange) []bundleFile {
	// Export the exact archived timepoint population used by the corrected OTP.
	stopEvents := fmt.Sprintf(`
WITH stops AS MATERIALIZED (
    SELECT t.schedule_id,t.trip_id,t.service_id,s.stop,s.ordinality,
           jsonb_array_length(t.stops) AS stop_count,
           count(*) OVER(PARTITION BY t.schedule_id,t.trip_id,s.stop->>'id') AS occurrences
    FROM transit.metric_trip t
    CROSS JOIN LATERAL jsonb_array_elements(t.stops) WITH ORDINALITY s(stop,ordinality)
    WHERE t.schedule_id IN(SELECT schedule_id FROM transit.metric_rebuild WHERE date BETWEEN '%s' AND '%s')
), points AS MATERIALIZED (
    SELECT * FROM stops WHERE (stop->>'timepoint')::boolean
      AND (stop->>'departure')::int >= 21600 AND (stop->>'departure')::int < 86400
      AND ordinality < stop_count AND occurrences=1
)
SELECT d.date,d.route_id,d.trip_id,d.headsign,d.stop_id,d.stop_sequence,
       d.departure_delay,d.arrival_delay,COALESCE(d.departure_delay,d.arrival_delay) AS delay_sec,
       (COALESCE(d.departure_delay,d.arrival_delay) BETWEEN %g AND %g) AS on_time,
       s.stop->>'departure' AS scheduled_departure_sec,d.last_updated
FROM transit.stop_delay d
JOIN transit.metric_rebuild r ON r.date=d.date
JOIN points s ON s.schedule_id=r.schedule_id AND s.trip_id=d.trip_id AND s.stop->>'id'=d.stop_id
WHERE d.date BETWEEN '%s' AND '%s'
  AND EXISTS(SELECT 1 FROM transit.metric_service ms WHERE ms.schedule_id=s.schedule_id AND ms.service_id=s.service_id AND ms.date=d.date)
ORDER BY d.date,d.trip_id,d.stop_sequence`, dr.From, dr.To, chunk.OTPEarlyLimit, chunk.OTPLateLimit, dr.From, dr.To)

	// cancellations and alerts are append-per-poll event logs: a record that
	// stays active gets re-inserted on every feed poll (one row per poll). The
	// live site relies on that history (MIN feed = first seen → cancel-notice
	// KPI; MAX feed = current snapshot), but for an export the repeats are pure
	// noise. Collapse to one row per logical record, carrying first_seen /
	// last_seen / poll_count so the timing the history encoded isn't lost.
	cancellations := fmt.Sprintf(`
SELECT trip_id, start_date, route_id, start_time, schedule_relationship,
       headsign, pattern_id, scheduled_last_arr_time,
       first_seen, last_seen, poll_count
FROM (
    SELECT DISTINCT ON (c.trip_id, c.start_date)
        c.trip_id, c.start_date, c.route_id, c.start_time, c.schedule_relationship,
        c.headsign, c.pattern_id, c.scheduled_last_arr_time,
        MIN(c.feed_timestamp) OVER w AS first_seen,
        MAX(c.feed_timestamp) OVER w AS last_seen,
        COUNT(*)             OVER w AS poll_count
    FROM transit.cancellation c
    WHERE c.start_date >= replace('%s','-','') AND c.start_date <= replace('%s','-','')
    WINDOW w AS (PARTITION BY c.trip_id, c.start_date)
    ORDER BY c.trip_id, c.start_date, c.feed_timestamp DESC
) q
ORDER BY first_seen`, dr.From, dr.To)

	alerts := fmt.Sprintf(`
SELECT alert_id, cause, effect, header, description, severity_level, url,
       active_start, active_end, affected_routes, affected_stops,
       first_seen, last_seen, poll_count
FROM (
    SELECT DISTINCT ON (a.alert_id)
        a.alert_id, a.cause, a.effect, a.header, a.description, a.severity_level, a.url,
        a.active_start, a.active_end, a.affected_routes, a.affected_stops,
        MIN(a.feed_timestamp) OVER w AS first_seen,
        MAX(a.feed_timestamp) OVER w AS last_seen,
        COUNT(*)             OVER w AS poll_count
    FROM transit.alert a
    WHERE (a.feed_timestamp AT TIME ZONE 'America/Thunder_Bay')::date >= '%s'
      AND (a.feed_timestamp AT TIME ZONE 'America/Thunder_Bay')::date <= '%s'
    WINDOW w AS (PARTITION BY a.alert_id)
    ORDER BY a.alert_id, a.feed_timestamp DESC
) q
ORDER BY first_seen`, dr.From, dr.To)

	return []bundleFile{
		{
			name: "metrics_chunks.csv",
			sql: fmt.Sprintf(
				"SELECT * FROM transit.route_band_chunk WHERE date >= '%s' AND date <= '%s' ORDER BY date, route_id, band",
				dr.From, dr.To),
		},
		{name: "timepoint_stop_events.csv", sql: stopEvents},
		{name: "cancellations.csv", sql: cancellations},
		{name: "alerts.csv", sql: alerts},
		{name: "metric_passages.csv", sql: fmt.Sprintf("SELECT * FROM transit.metric_passage WHERE date BETWEEN '%s' AND '%s' ORDER BY date,trip_id,stop_id", dr.From, dr.To)},
		{name: "metric_rebuilds.csv", sql: fmt.Sprintf("SELECT r.*,s.source,s.published_at FROM transit.metric_rebuild r JOIN transit.metric_schedule s ON s.id=r.schedule_id WHERE date BETWEEN '%s' AND '%s' ORDER BY date", dr.From, dr.To)},
		{name: "metric_timetables.csv", sql: fmt.Sprintf("SELECT * FROM transit.metric_trip WHERE schedule_id IN(SELECT schedule_id FROM transit.metric_rebuild WHERE date BETWEEN '%s' AND '%s') ORDER BY schedule_id,trip_id", dr.From, dr.To)},
		{name: "metric_calendar.csv", sql: fmt.Sprintf("SELECT * FROM transit.metric_service WHERE date BETWEEN '%s' AND '%s' ORDER BY date,schedule_id,service_id", dr.From, dr.To)},
	}
}

// dataDownload streams a ZIP of curated CSVs bounded by the ?from=&to= range.
// parseDateRange is the single validation layer: it clamps to today, swaps a
// reversed range, and caps the span at MaxRangeDays (one year) — so a download
// can never ask for more than a year regardless of what the URL says. Each CSV
// is generated inside Postgres via COPY ... TO STDOUT and streamed straight
// into the zip entry — no temp files, flat memory regardless of table size.
func (h *Handler) dataDownload(w http.ResponseWriter, r *http.Request) {
	dr := parseDateRange(r, "")

	// Clear the server-wide 15s WriteTimeout for this route. The bundle streams
	// to the client as it's generated, so a large range over a slow link easily
	// exceeds 15s; without this the connection is force-closed mid-stream and
	// the client gets a truncated, unopenable ZIP. Scoped per-connection — other
	// routes keep the global timeout. Mirrors vehiclesSSE.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		downloadLog.Warn("download: could not clear write deadline", "err", err)
	}

	name := fmt.Sprintf("thunder-transit-data-%s-to-%s.zip", dr.From, dr.To)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Cache-Control", cache.Short)

	if err := h.svc.writeDataBundle(r.Context(), w, dr); err != nil {
		// The status line and some zip bytes are already on the wire, so we
		// can't switch to a clean error response. The truncated archive fails
		// to open, which surfaces the error to the client.
		downloadLog.Error("data bundle failed", "from", dr.From, "to", dr.To, "err", err)
	}
}

// writeDataBundle writes the CSV set plus a README into a streaming ZIP. All
// COPYs share a repeatable-read snapshot, so a concurrent rollup cannot mix
// old counts with new passages or timetable provenance in the same export.
func (s *Service) writeDataBundle(ctx context.Context, w io.Writer, dr DateRange) error {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	zw := zip.NewWriter(w)
	for _, f := range bundleFiles(dr) {
		entry, err := zw.Create(f.name)
		if err != nil {
			return err
		}
		copySQL := fmt.Sprintf("COPY (%s) TO STDOUT WITH (FORMAT csv, HEADER true)", f.sql)
		if _, err := conn.Conn().PgConn().CopyTo(ctx, entry, copySQL); err != nil {
			return err
		}
	}

	readme, err := zw.Create("README.txt")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(readme, bundleReadme(dr)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	return zw.Close()
}

func bundleReadme(dr DateRange) string {
	return fmt.Sprintf(`Thunder Citizen — Transit data export
Date range: %s to %s (service dates, America/Thunder_Bay)
Source: unofficial, derived from observing Thunder Bay Transit's GTFS feeds.

FORMULAS (metric_version=1 only)
OTP = 100 * sum(otp_on_time) / sum(otp_count).
A sample is one reported timepoint departure, not a trip-average delay.
The window is [%g, %g] seconds. Departure delay takes precedence; arrival
is the fallback. GTFS-RT updates may be predictions, not verified departures.
Cancellation rate = reported cancelled trips / scheduled trips * 100.
Cancellation-free chunks without observed trips do not establish a zero rate.

For each route, sum wait_observed_area and wait_scheduled_area, then divide
their difference by sum(window_seconds) and by 60 to get EWT minutes.
For each route, CV = sum(cv_weighted_sum) / sum(cv_weight).
Average routes with a denominator equally; no passenger weights are available.
Do not pool raw headways from different stops/directions/timetables for CV.
Negative EWT is retained. Missing data is not zero.

FILES
metrics_chunks.csv: corrected counts, wait integrals and completeness counts.
  Old trip-average OTP and headway columns are retained for schema compatibility;
  corrected rows do not use them. Version 0 rows require rebuilding.
timepoint_stop_events.csv: archived-schedule timepoints behind OTP.
cancellations.csv: one row per reported trip/date, with first/last observation.
alerts.csv: one row per alert, carrying its latest content.
metric_passages.csv: screened GPS passage estimates and source GPS row IDs.
metric_rebuilds.csv: recipe version and timetable provenance for every date.
metric_timetables.csv: archived trips, with ordered stop schedules as JSON.
metric_calendar.csv: active service IDs for each timetable/date.

REGULARITY COVERAGE
Only interior timepoints with at least three scheduled passages are candidates.
Every scheduled passage must be observed or explicitly reported cancelled.
Unknown telemetry suppresses the entire stop/direction/day/band window.
Actual and scheduled waits are integrated over the same bounded time interval.
Endpoint/terminal ambiguity, repeated stops, ambiguous GPS episodes and
nonmonotone trips are excluded. No short/long gap is removed by duration alone.
Coverage-dependent selection remains: these are estimates for sampled windows,
not proof of system-wide passenger waiting time.

Times are in America/Thunder_Bay. Source vehicle GPS records are not included
because of their size; source IDs support checking against the retained archive.
Downloads are capped at one year. See docs/transit-metrics.md for rebuild steps.
`, dr.From, dr.To, chunk.OTPEarlyLimit, chunk.OTPLateLimit)
}
