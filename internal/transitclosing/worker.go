// Package transitclosing closes operating months with a durable, retryable journal.
package transitclosing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/logger"
	"thundercitizen/internal/transit"
	"thundercitizen/internal/transit/chunk"
	"thundercitizen/internal/transitarchive"
)

var log = logger.New("transit_close")

type Worker struct {
	db    *pgxpool.Pool
	store Store
	// Injected in integration tests to keep fixture schedules small.
	finalize  func(context.Context, time.Time) error
	preflight func(context.Context, time.Time) error
}

func New(db *pgxpool.Pool, store Store) *Worker {
	w := &Worker{db: db, store: store}
	w.finalize = w.finalizeMonth
	w.preflight = w.checkScheduleCoverage
	return w
}
func (w *Worker) Start(ctx context.Context) {
	go func() {
		for {
			if err := w.RunOnce(ctx, time.Now()); err != nil && ctx.Err() == nil {
				log.Error("monthly close failed; will retry", "err", err)
			}
			timer := time.NewTimer(nextWake(time.Now()).Sub(time.Now()))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

// Wake at the local operating-day boundary, with short retries/catch-up polls.
func nextWake(now time.Time) time.Time {
	local := now.In(transit.TZ)
	boundary := time.Date(local.Year(), local.Month(), local.Day(), 4, 0, 0, 0, transit.TZ)
	if !boundary.After(now) {
		boundary = boundary.AddDate(0, 0, 1)
	}
	retry := now.Add(5 * time.Minute)
	if boundary.Before(retry) {
		return boundary
	}
	return retry
}
func (w *Worker) RunOnce(ctx context.Context, now time.Time) error {
	conn, err := w.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// Only one server instance coordinates closing; restart releases the lock.
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(23012,0)`).Scan(&locked); err != nil {
		// Cancellation can race acquisition; never return a potentially locked
		// session to the pool when the acquisition result is unknown.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Conn().Close(cleanup)
		cancel()
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(c, `SELECT pg_advisory_unlock(23012,0)`); err != nil {
			_ = conn.Conn().Close(c)
		}
	}()
	if err = transitarchive.EnsurePartitions(ctx, conn.Conn(), now); err != nil {
		return err
	}
	if w.store == nil {
		return nil
	}
	months, err := transitarchive.PartitionMonths(ctx, conn.Conn())
	if err != nil {
		return err
	}
	for _, month := range months {
		m, _, err := transitarchive.Bounds(month, now, 0)
		if err != nil {
			continue
		}
		if err = w.closeMonth(ctx, conn.Conn(), m); err != nil {
			// A cancelled context must still leave a useful failure record.
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = conn.Exec(c, `UPDATE transit.archive_month SET last_error=$2,updated_at=now() WHERE month=$1`, m, err.Error())
			cancel()
			return fmt.Errorf("close %s: %w", month, err)
		}
	}
	return nil
}
func (w *Worker) closeMonth(ctx context.Context, conn *pgx.Conn, month time.Time) error {
	if _, err := conn.Exec(ctx, `INSERT INTO transit.archive_month(month,state,metric_version) VALUES($1,'preparing',$2) ON CONFLICT DO NOTHING`, month, chunk.Version); err != nil {
		return err
	}
	var state string
	var version int
	if err := conn.QueryRow(ctx, `UPDATE transit.archive_month SET attempts=attempts+1,last_error=NULL,updated_at=now() WHERE month=$1 RETURNING state,metric_version`, month).Scan(&state, &version); err != nil {
		return err
	}
	if state == "complete" {
		return nil
	}
	if version != chunk.Version {
		return fmt.Errorf("month uses recipe version %d; finish its close with that version before upgrading", version)
	}
	// A newly introduced derived table may have no partition for this older
	// month. Also recover default-partition rows from interrupted older closers.
	if err := transitarchive.EnsureMonthPartitions(ctx, conn, month); err != nil {
		return err
	}
	log.Info("closing operating month", "month", month.Format("2006-01"), "stage", state)
	if state == "preparing" {
		if err := w.preflight(ctx, month); err != nil {
			return err
		}
		if err := transition(ctx, conn, month, `UPDATE transit.archive_month SET state='sealed',sealed_at=now(),updated_at=now() WHERE month=$1`); err != nil {
			return err
		}
		state = "sealed"
	}
	if state == "sealed" {
		if err := w.finalize(ctx, month); err != nil {
			return err
		}
		if err := transition(ctx, conn, month, `UPDATE transit.archive_month SET state='frozen',frozen_at=now(),updated_at=now() WHERE month=$1`); err != nil {
			return err
		}
		state = "frozen"
	}
	if state == "frozen" {
		if err := w.upload(ctx, conn, month); err != nil {
			return err
		}
		state = "uploaded"
	}
	if state == "uploaded" {
		// This commit is the sole public release switch. All objects already exist.
		if _, err := conn.Exec(ctx, `UPDATE transit.archive_month SET state='published',published_at=now(),updated_at=now() WHERE month=$1`, month); err != nil {
			return err
		}
		state = "published"
	}
	if state == "published" {
		return w.evict(ctx, conn, month)
	}
	return nil
}
func transition(ctx context.Context, conn *pgx.Conn, month time.Time, sql string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(23010,$1)`, month.Year()*12+int(month.Month())); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, sql, month); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (w *Worker) finalizeMonth(ctx context.Context, month time.Time) error {
	// Rebuild every scheduled or observed service day, not only the last day.
	// If archived schedules are missing, fail closed and retain the raw partition.
	rows, err := w.db.Query(ctx, `SELECT date FROM transit.metric_service s WHERE date >= $1::date AND date < $2::date AND EXISTS(SELECT 1 FROM transit.metric_trip t WHERE t.schedule_id=s.schedule_id AND t.service_id=s.service_id)
 UNION SELECT date FROM transit.stop_delay WHERE date >= $1::date AND date < $2::date
 UNION SELECT to_date(start_date,'YYYYMMDD') FROM transit.cancellation WHERE start_date >= to_char($1::date,'YYYYMMDD') AND start_date < to_char($2::date,'YYYYMMDD')
 ORDER BY 1`, month, month.AddDate(0, 1, 0))
	if err != nil {
		return err
	}
	var days []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			rows.Close()
			return err
		}
		days = append(days, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Pre-provisioned months with no service can close with no metric rows.
	// Preflight has already rejected observed service lacking a timetable.
	for _, day := range days {
		date := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, transit.TZ)
		if _, err := transit.RebuildMetricPassages(ctx, w.db, date); err != nil {
			return err
		}
		if _, err := transit.BuildChunksForDate(ctx, w.db, date); err != nil {
			return err
		}
	}
	return nil
}
func (w *Worker) upload(ctx context.Context, conn *pgx.Conn, month time.Time) error {
	root, err := os.MkdirTemp("", "transit-close-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	var savedPrefix *string
	var savedManifest []byte
	var verifiedAt *time.Time
	if err = conn.QueryRow(ctx, `SELECT raw_prefix,raw_manifest,raw_verified_at FROM transit.archive_month WHERE month=$1`, month).Scan(&savedPrefix, &savedManifest, &verifiedAt); err != nil {
		return err
	}
	release := ""
	if savedPrefix != nil || savedManifest != nil || verifiedAt != nil {
		if savedPrefix == nil || len(savedManifest) == 0 || verifiedAt == nil {
			return fmt.Errorf("incomplete verified raw checkpoint for %s", month.Format("2006-01"))
		}
		// JSONB canonicalizes the manifest, so its original byte hash must come
		// from the saved object prefix rather than reserializing the manifest.
		release = path.Base(*savedPrefix)
		decoded, decodeErr := hex.DecodeString(release)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("invalid verified raw checkpoint release for %s", month.Format("2006-01"))
		}
	} else {
		dir := filepath.Join(root, "raw")
		m, err := transitarchive.ExportClosed(ctx, conn, dir, month.Format("2006-01"))
		if err != nil {
			return err
		}
		if _, err = transitarchive.Verify(dir); err != nil {
			return err
		}
		manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			return err
		}
		hash := sha256.Sum256(manifest)
		release = hex.EncodeToString(hash[:])
		rawPrefix := "transit/raw/" + month.Format("2006-01") + "/" + release
		for _, table := range m.Tables {
			if _, err = w.store.UploadVerified(ctx, rawPrefix+"/"+table.File, filepath.Join(dir, table.File), false); err != nil {
				return err
			}
		}
		if _, err = w.store.UploadVerified(ctx, rawPrefix+"/manifest.json", filepath.Join(dir, "manifest.json"), false); err != nil {
			return err
		}
		// This checkpoint is durable before the public bundle is attempted.
		// Frozen month rows cannot change, and publication still requires the verified
		// bundle. A failed report upload can therefore reuse verified raw data.
		if _, err = conn.Exec(ctx, `UPDATE transit.archive_month SET raw_prefix=$2,raw_manifest=$3::jsonb,raw_verified_at=now(),updated_at=now() WHERE month=$1`, month, w.store.PublicURL(rawPrefix), string(manifest)); err != nil {
			return err
		}
	}
	name := transit.ReportBundleFilename()
	f, err := os.Create(filepath.Join(root, name))
	if err != nil {
		return err
	}
	err = transit.WriteReportBundleBefore(ctx, conn, f, month.AddDate(0, 1, 0))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Each immutable ZIP is archived from the start. Publishing the new DB
	// pointer replaces the current download without moving/deleting older files.
	reportPrefix := "transit/reports/archive/" + month.Format("2006-01") + "/" + release
	obj, err := w.store.UploadVerified(ctx, reportPrefix+"/"+name, filepath.Join(root, name), true)
	if err != nil {
		return err
	}
	objects := []Object{obj}
	reports, err := json.Marshal(objects)
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, `UPDATE transit.archive_month SET state='uploaded',report_base_url=$2,report_manifest=$3::jsonb,updated_at=now() WHERE month=$1`, month, w.store.PublicURL(reportPrefix), string(reports))
	return err
}
func (w *Worker) evict(ctx context.Context, conn *pgx.Conn, month time.Time) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// A busy live reader should defer cleanup, not hold the whole recorder
	// behind a queued parent-table DDL lock for an unbounded period.
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(23010,$1)`, month.Year()*12+int(month.Month())); err != nil {
		return err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT raw_manifest FROM transit.archive_month WHERE month=$1 FOR UPDATE`, month).Scan(&raw); err != nil {
		return err
	}
	var m transitarchive.Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m.Month != month.Format("2006-01") {
		return fmt.Errorf("archive manifest month mismatch")
	}
	// Refuse removal unless every partition's entire contents are represented.
	for _, table := range transitarchive.PartitionTables {
		predicate := transitarchive.PartitionPredicate(table, month)
		var found *transitarchive.Table
		for i := range m.Tables {
			t := &m.Tables[i]
			if t.Schema == "transit" && t.Name == table.Name {
				found = t
				break
			}
		}
		if found == nil || found.Predicate != predicate {
			return fmt.Errorf("archive does not cover %s", table.Name)
		}
		var count int64
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{"transit", table.Name}.Sanitize()+" WHERE "+predicate).Scan(&count); err != nil {
			return err
		}
		if count != found.Rows {
			return fmt.Errorf("%s changed since archive: %d rows, expected %d", table.Name, count, found.Rows)
		}
	}
	if err = transitarchive.DropMonthPartitions(ctx, tx, month.Format("2006-01")); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE transit.archive_month SET state='complete',completed_at=now(),updated_at=now(),last_error=NULL WHERE month=$1`, month); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Do not seal a month while startup schedule import/recovery is still pending.
// GPS without a trip identity and alerts alone cannot establish service dates.
func (w *Worker) checkScheduleCoverage(ctx context.Context, month time.Time) error {
	end := month.AddDate(0, 1, 0)
	startGPS := time.Date(month.Year(), month.Month(), 1, 4, 0, 0, 0, transit.TZ)
	endGPS := startGPS.AddDate(0, 1, 0)
	var missing *time.Time
	err := w.db.QueryRow(ctx, `WITH observed AS (
 SELECT DISTINCT date FROM transit.stop_delay WHERE date >= $1::date AND date < $2::date
 UNION SELECT DISTINCT to_date(start_date,'YYYYMMDD') FROM transit.cancellation WHERE start_date >= to_char($1::date,'YYYYMMDD') AND start_date < to_char($2::date,'YYYYMMDD')
 UNION SELECT DISTINCT ((feed_timestamp AT TIME ZONE 'America/Thunder_Bay')-interval '4 hours')::date FROM transit.vehicle_position WHERE feed_timestamp >= $3 AND feed_timestamp < $4 AND trip_id IS NOT NULL
 ) SELECT min(date) FROM observed o WHERE NOT EXISTS(
 SELECT 1 FROM transit.metric_service s JOIN transit.metric_trip t ON t.schedule_id=s.schedule_id AND t.service_id=s.service_id WHERE s.date=o.date
 )`, month, end, startGPS, endGPS).Scan(&missing)
	if err != nil {
		return err
	}
	if missing != nil {
		return fmt.Errorf("archived timetable coverage missing for %s; import the historical GTFS before sealing", missing.Format("2006-01-02"))
	}
	return nil
}
