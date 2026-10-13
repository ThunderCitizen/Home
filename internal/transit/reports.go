package transit

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"thundercitizen/internal/config"
	"thundercitizen/internal/transit/chunk"
)

// Public filenames carry the metric recipe version; CSV rows are presentation data.
func MonthlyReportFilename() string { return fmt.Sprintf("monthly_metrics_v%d.csv", chunk.Version) }
func DailyReportFilename() string   { return fmt.Sprintf("route_day_summaries_v%d.csv", chunk.Version) }

func ReportBundleFilename() string { return fmt.Sprintf("transit_reports_v%d.zip", chunk.Version) }

// WriteReportBundleBefore exports the trailing 12 completed months as one ZIP.
// Entry order and timestamps are deterministic so retries preserve its checksum.
func WriteReportBundleBefore(ctx context.Context, conn *pgx.Conn, out io.Writer, cutoff time.Time) error {
	var monthly, daily bytes.Buffer
	if err := WriteReportsBefore(ctx, conn, &monthly, &daily, cutoff); err != nil {
		return err
	}
	return writeReportZIP(out, monthly.Bytes(), daily.Bytes())
}

func WriteReportBundle(ctx context.Context, conn *pgx.Conn, out io.Writer, now time.Time) error {
	day := serviceDateAt(now)
	return WriteReportBundleBefore(ctx, conn, out, time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, TZ))
}

func writeReportZIP(out io.Writer, monthly, daily []byte) error {
	z := zip.NewWriter(out)
	for i, name := range []string{MonthlyReportFilename(), DailyReportFilename()} {
		entry, err := z.Create(name)
		if err != nil {
			z.Close()
			return err
		}
		if _, err = entry.Write([][]byte{monthly, daily}[i]); err != nil {
			z.Close()
			return err
		}
	}
	return z.Close()
}

type reportRoute struct{ Number, Name string }

// ReportBaseURL points at a published, immutable report ZIP directory.
func ReportBaseURL() string {
	raw := strings.TrimRight(config.Secret("TRANSIT_REPORT_BASE_URL", ""), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return raw
}

// WriteConsolidatedReports reads only retained daily aggregates in one snapshot.
// Current service-month rows are excluded; stale or provisional retained rows
// must be repaired before publishing. Missing days are not invented.
func WriteConsolidatedReports(ctx context.Context, conn *pgx.Conn, monthly, daily io.Writer, now time.Time) error {
	day := serviceDateAt(now)
	cutoff := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, TZ)
	return WriteReportsBefore(ctx, conn, monthly, daily, cutoff)
}

// WriteReportsBefore builds one release through the last closed month, excluding
// later completed months that a catch-up worker has not finalized yet. Only the
// trailing 12 completed calendar months are included; retained DB history is unchanged.
func WriteReportsBefore(ctx context.Context, conn *pgx.Conn, monthly, daily io.Writer, cutoff time.Time) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT to_jsonb(d) || jsonb_build_object('trips',trip_count,'scheduled',cancellation_scheduled_count,'cancelled',cancelled_count,'no_notice',no_notice_count) , COALESCE(NULLIF(r.short_name,''),d.route_id), COALESCE(NULLIF(r.long_name,''),NULLIF(r.short_name,''),d.route_id) FROM transit.route_day_summary d LEFT JOIN transit.route r ON r.route_id=d.route_id WHERE d.date >= $2::date AND d.date < $1::date ORDER BY d.date,d.route_id`, cutoff, cutoff.AddDate(-1, 0, 0))
	if err != nil {
		return err
	}
	var all []chunk.ChunkView
	routes := map[string]reportRoute{}
	for rows.Next() {
		var raw []byte
		var route reportRoute
		if err := rows.Scan(&raw, &route.Number, &route.Name); err != nil {
			rows.Close()
			return err
		}
		var d struct {
			chunk.ChunkView
			IsFinal bool `json:"is_final"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			rows.Close()
			return err
		}
		if !d.IsFinal || d.Version != chunk.Version {
			rows.Close()
			return fmt.Errorf("repair unfinished or stale summary for %s/%s before publication", d.Date, d.RouteID)
		}
		all = append(all, d.ChunkView)
		routes[d.RouteID] = route
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return writeReportCSVs(monthly, daily, all, routes)
}

func writeReportCSVs(monthly, daily io.Writer, all []chunk.ChunkView, routes map[string]reportRoute) error {
	dw, mw := csv.NewWriter(daily), csv.NewWriter(monthly)
	dw.Write([]string{"Date", "Route", "Route name", "Trips", "Cancelled", "OTP (%)"})
	months := map[string][]chunk.ChunkView{}
	n := strconv.Itoa
	for _, d := range all {
		one := []chunk.ChunkView{d}
		route := routes[d.RouteID]
		if route.Number == "" {
			route.Number = d.RouteID
		}
		if route.Name == "" {
			route.Name = route.Number
		}
		card := RouteRowKPIFromChunks(one, d.RouteID)
		otp := card.OTP
		if otp == "—" {
			otp = ""
		}
		dw.Write([]string{d.Date, route.Number, route.Name, card.Trips, card.Cancel, otp})
		months[d.Date[:7]] = append(months[d.Date[:7]], d)
	}
	mw.Write([]string{"Month", "Days with data", "Routes with data", "Trips", "Cancelled", "Cancelled (%)", "OTP (%)", "Short notice (%)", "Average wait (min)", "Excess wait (min)", "Headway CV"})
	keys := make([]string, 0, len(months))
	for k := range months {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, m := range keys {
		days, routes := map[string]bool{}, map[string]bool{}
		trips, cancel := 0, 0
		for _, d := range months[m] {
			days[d.Date] = true
			routes[d.RouteID] = true
			trips += d.Trips
			cancel += d.Cancelled
		}
		row := []string{m, n(len(days)), n(len(routes)), n(trips), n(cancel)}
		for _, metric := range []chunk.Metric{chunk.MetricCancel, chunk.MetricOTP, chunk.MetricNotice, chunk.MetricWait, chunk.MetricEWT, chunk.MetricCv} {
			row = append(row, reportKPI(months[m], metric))
		}
		mw.Write(row)
	}
	dw.Flush()
	mw.Flush()
	if err := dw.Error(); err != nil {
		return err
	}
	return mw.Error()
}
func reportKPI(rows []chunk.ChunkView, metric chunk.Metric) string {
	v, ok := chunk.KPI(rows, metric, "")
	if !ok {
		return ""
	}
	return formatKPI(metric, v, true)
}

// PublishedReportBaseURL switches the ZIP download in one journal commit.
// The explicit configuration setting remains a fallback for manually published
// releases until the first automatic close succeeds.
func (s *Service) PublishedReportBaseURL(ctx context.Context) (string, error) {
	var base string
	err := s.db.QueryRow(ctx, `SELECT report_base_url FROM transit.archive_month WHERE state IN ('published','complete') ORDER BY month DESC LIMIT 1`).Scan(&base)
	if err == pgx.ErrNoRows {
		return ReportBaseURL(), nil
	}
	return base, err
}

// PublishedReportDownloadURL uses the recorded artifact name, so a recipe-version
// upgrade cannot break last month's download before its replacement is ready.
func (s *Service) PublishedReportDownloadURL(ctx context.Context) (string, error) {
	var base string
	var manifest []byte
	err := s.db.QueryRow(ctx, `SELECT report_base_url,report_manifest FROM transit.archive_month WHERE state IN ('published','complete') ORDER BY month DESC LIMIT 1`).Scan(&base, &manifest)
	if err == pgx.ErrNoRows {
		if base = ReportBaseURL(); base != "" {
			return base + "/" + ReportBundleFilename(), nil
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var objects []struct{ Key string }
	if err := json.Unmarshal(manifest, &objects); err != nil {
		return "", err
	}
	for _, obj := range objects {
		name := path.Base(obj.Key)
		if strings.HasSuffix(name, ".zip") {
			return strings.TrimRight(base, "/") + "/" + name, nil
		}
	}
	return "", fmt.Errorf("published release has no ZIP artifact")
}
