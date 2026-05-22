package transit

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"thundercitizen/internal/cache"
	"thundercitizen/internal/logger"
	"thundercitizen/internal/transit/chunk"
)

var downloadLog = logger.New("download")

// avgBundleBytesPerDay is the per-day size of the compressed export ZIP, used
// only for the "estimated size" shown in the download dialog. The bundle is
// dominated by timepoint_stop_events, which scales ~linearly with service days,
// so days × constant is close enough for a pre-download heads-up. Calibrated
// against a real production week (~540 KB / 7 days). Recalibrate by downloading
// a week and dividing by 7.
const avgBundleBytesPerDay = 77 * 1024

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
// Excluded on purpose: the raw GPS firehose (transit.vehicle_position) — polled
// every six seconds, gigabytes per year, useless as a flat dump — and
// transit.stop_visit, a derived convenience table redundant with the timepoint
// stop events below.
func bundleFiles(dr DateRange) []bundleFile {
	// Timepoint membership is resolved via route_pattern_stop, NOT
	// stop_delay.is_timepoint — the recorder leaves that column false because
	// its trip cache isn't keyed by stop. This mirrors recipes/otp.go exactly,
	// so the exported rows are the same population the OTP metric is built from.
	stopEvents := fmt.Sprintf(`
SELECT
    d.date, d.route_id, d.trip_id, d.headsign,
    d.stop_id, d.stop_sequence, d.band, d.service_kind,
    d.arrival_delay, d.departure_delay,
    COALESCE(d.arrival_delay, d.departure_delay) AS delay_sec,
    (COALESCE(d.arrival_delay, d.departure_delay) BETWEEN %g AND %g) AS on_time,
    d.is_first_stop, d.scheduled_first_dep_time, d.last_updated
FROM transit.stop_delay d
WHERE d.date >= '%s' AND d.date <= '%s'
  AND EXISTS (
    SELECT 1 FROM transit.route_pattern_stop rps
    WHERE rps.pattern_id = d.pattern_id
      AND rps.stop_id = d.stop_id
      AND rps.is_timepoint = true
  )
ORDER BY d.date, d.trip_id, d.stop_sequence`,
		chunk.OTPEarlyLimit, chunk.OTPLateLimit, dr.From, dr.To)

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
    WHERE (c.feed_timestamp AT TIME ZONE 'America/Thunder_Bay')::date >= '%s'
      AND (c.feed_timestamp AT TIME ZONE 'America/Thunder_Bay')::date <= '%s'
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
// COPYs run on one acquired connection, sequentially, so the pool sees a single
// checkout for the whole download.
func (s *Service) writeDataBundle(ctx context.Context, w io.Writer, dr DateRange) error {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	zw := zip.NewWriter(w)
	for _, f := range bundleFiles(dr) {
		entry, err := zw.Create(f.name)
		if err != nil {
			zw.Close()
			return err
		}
		copySQL := fmt.Sprintf("COPY (%s) TO STDOUT WITH (FORMAT csv, HEADER true)", f.sql)
		if _, err := conn.Conn().PgConn().CopyTo(ctx, entry, copySQL); err != nil {
			zw.Close()
			return err
		}
	}

	readme, err := zw.Create("README.txt")
	if err != nil {
		zw.Close()
		return err
	}
	if _, err := io.WriteString(readme, bundleReadme(dr)); err != nil {
		zw.Close()
		return err
	}

	return zw.Close()
}

func bundleReadme(dr DateRange) string {
	return fmt.Sprintf(`Thunder Citizen — Transit data export
Date range: %s to %s (service dates, America/Thunder_Bay)
Source: unofficial, derived from observing Thunder Bay Transit's GTFS feeds.

These files are the minimum set to reproduce — or redefine — every metric on
the Metrics tab in a spreadsheet. All counts are raw; percentages are never
stored, so you sum the counts and divide once yourself.

FILES
-----
metrics_chunks.csv
    Pre-rolled aggregates: one row per route x service-day x 6-hour band.
    Columns hold raw counts (trip_count, on_time_count, scheduled_count,
    cancelled_count, no_notice_count) and SUM-stable headway sums
    (headway_count, headway_sum_sec, headway_sum_sec_sq, sched_headway_sec).
    This is the "answer key": aggregate it across rows for any KPI.
      OTP            = sum(on_time_count) / sum(trip_count)
      Cancel rate    = sum(cancelled_count) / sum(scheduled_count)
      Headway CV     = sqrt(headway_sum_sec_sq/headway_count
                           - (headway_sum_sec/headway_count)^2)
                       / (headway_sum_sec/headway_count), per route, then
                       averaged across routes.

timepoint_stop_events.csv
    The raw layer behind OTP. One row per trip per timepoint stop (timepoint
    membership already applied). delay_sec = arrival_delay, falling back to
    departure_delay. on_time is OUR classification: delay_sec within
    [%g, %g] seconds (1 min early to 5 min late). To use your own
    definition, ignore on_time and threshold delay_sec however you like.
    Our official OTP is per-TRIP: average delay_sec across a trip's timepoint
    stops, then apply the window — group by (date, trip_id) to reproduce it.

cancellations.csv
    One row per cancelled trip (trip_id + start_date). We observe these on
    every feed poll while the trip stays cancelled; rows are de-duplicated to
    one per trip, with first_seen / last_seen (when we first/last saw it) and
    poll_count (how many polls reported it). Notice lead = scheduled departure
    minus first_seen.

alerts.csv
    One row per service alert (alert_id), de-duplicated the same way with
    first_seen / last_seen / poll_count. Content columns (header, description,
    affected_routes, ...) reflect the most recent poll.

NOTES
-----
- Times in *_delay columns are seconds (negative = early).
- first_seen / last_seen are timestamps with timezone (UTC offset shown).
- Downloads are capped at one year per request.
- Raw vehicle GPS positions are not included: that feed is polled every six
  seconds and runs to gigabytes per year.
`, dr.From, dr.To, chunk.OTPEarlyLimit, chunk.OTPLateLimit)
}
