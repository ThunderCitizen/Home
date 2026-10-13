// Package transitarchive provides verified monthly snapshots, analysis restores,
// and partition lifecycle primitives gated by the automatic close journal.
package transitarchive

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type Table struct {
	Schema    string   `json:"schema"`
	Name      string   `json:"name"`
	Columns   []Column `json:"columns"`
	File      string   `json:"file"`
	SHA256    string   `json:"sha256"`
	Rows      int64    `json:"rows"`
	Predicate string   `json:"predicate"`
}
type Manifest struct {
	Version          int       `json:"version"`
	Month            string    `json:"month"`
	CreatedAt        time.Time `json:"created_at"`
	DatabaseVersion  string    `json:"database_version"`
	MigrationVersion int64     `json:"migration_version"`
	Tables           []Table   `json:"tables"`
}

// Bounds uses local service dates, including the 04:00 service-day boundary.
func Bounds(month string, now time.Time, grace time.Duration) (time.Time, time.Time, error) {
	loc, err := time.LoadLocation("America/Thunder_Bay")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil {
		return start, start, err
	}
	end := start.AddDate(0, 1, 0)
	boundary := time.Date(end.Year(), end.Month(), end.Day(), 4, 0, 0, 0, loc)
	if grace < 0 || now.Before(boundary.Add(grace)) {
		return start, end, fmt.Errorf("month is not complete plus service-day boundary and grace period")
	}
	return start, end, nil
}

func predicate(name string, start, end time.Time) string {
	dates := func(col string) string {
		return fmt.Sprintf("%s >= '%s' AND %s < '%s'", col, start.Format("2006-01-02"), col, end.Format("2006-01-02"))
	}
	timestamps := func(col string) string {
		return fmt.Sprintf("%s >= '%s'::timestamptz AND %s < '%s'::timestamptz", col, start.AddDate(0, 0, -1).Format(time.RFC3339), col, end.AddDate(0, 0, 1).Format(time.RFC3339))
	}
	switch name {
	case "stop_delay", "vehicle_assignment", "metric_passage", "route_band_chunk", "route_day_summary", "metric_rebuild":
		return dates("date")
	case "stop_visit", "stop_visit_duplicate_archive":
		return timestamps("observed_at")
	case "vehicle_position", "alert":
		return timestamps("feed_timestamp")
	case "cancellation":
		return fmt.Sprintf("(start_date >= '%s' AND start_date < '%s') OR (%s)", start.Format("20060102"), end.Format("20060102"), timestamps("feed_timestamp"))
	case "feed_gap":
		return fmt.Sprintf("gap_start < '%s'::timestamptz AND gap_end >= '%s'::timestamptz", end.AddDate(0, 0, 1).Format(time.RFC3339), start.AddDate(0, 0, -1).Format(time.RFC3339))
	default:
		return "TRUE" // Reference tables are copied in full, including schedule provenance.
	}
}

func columns(ctx context.Context, tx pgx.Tx, schema, name string) ([]Column, error) {
	rows, err := tx.Query(ctx, `SELECT a.attname,pg_catalog.format_type(a.atttypid,a.atttypmod) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func columnSQL(cols []Column) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = pgx.Identifier{c.Name}.Sanitize()
	}
	return strings.Join(out, ",")
}

// Export creates a new directory; incomplete exports never contain a manifest.
// Every table is read from one repeatable-read snapshot, including references.
func Export(ctx context.Context, conn *pgx.Conn, dir, month string, grace time.Duration) (*Manifest, error) {
	return export(ctx, conn, dir, month, grace, false)
}

// ExportClosed exports exactly the owned operating-month raw partitions. The
// caller must freeze that month before invoking it; unlike evidence snapshots,
// it includes no padded observations from adjacent operating months.
func ExportClosed(ctx context.Context, conn *pgx.Conn, dir, month string) (*Manifest, error) {
	return export(ctx, conn, dir, month, 0, true)
}
func export(ctx context.Context, conn *pgx.Conn, dir, month string, grace time.Duration, closed bool) (*Manifest, error) {
	start, end, err := Bounds(month, time.Now(), grace)
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL TIME ZONE 'UTC'; SET LOCAL DateStyle = 'ISO, YMD'; SET LOCAL IntervalStyle = 'postgres'`); err != nil {
		return nil, err
	}
	m := &Manifest{Version: 1, Month: month, CreatedAt: time.Now().UTC()}
	if err = tx.QueryRow(ctx, `SHOW server_version`).Scan(&m.DatabaseVersion); err != nil {
		return nil, err
	}
	var dirty bool
	if err = tx.QueryRow(ctx, `SELECT version,dirty FROM public.schema_migrations`).Scan(&m.MigrationVersion, &dirty); err != nil {
		return nil, err
	}
	if dirty {
		return nil, fmt.Errorf("database has dirty migration state")
	}
	rows, err := tx.Query(ctx, `SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ('transit','gtfs') AND c.relkind IN ('r','p') AND NOT c.relispartition ORDER BY n.nspname,c.relname`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t Table
		if err = rows.Scan(&t.Schema, &t.Name); err != nil {
			rows.Close()
			return nil, err
		}
		m.Tables = append(m.Tables, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range m.Tables {
		t := &m.Tables[i]
		t.Columns, err = columns(ctx, tx, t.Schema, t.Name)
		if err != nil {
			return nil, err
		}
		t.Predicate = "TRUE"
		if t.Schema == "transit" {
			t.Predicate = predicate(t.Name, start, end)
			if closed {
				for _, table := range PartitionTables {
					if t.Name == table.Name {
						t.Predicate = PartitionPredicate(table, start)
					}
				}
			}
		}
		t.File = t.Schema + "__" + t.Name + ".csv.gz"
		f, e := os.OpenFile(filepath.Join(dir, t.File), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		h := sha256.New()
		gz := gzip.NewWriter(io.MultiWriter(f, h))
		tag, e := tx.Conn().PgConn().CopyTo(ctx, gz, fmt.Sprintf("COPY (SELECT %s FROM %s WHERE %s) TO STDOUT WITH (FORMAT csv, NULL '\\N', FORCE_QUOTE *)", columnSQL(t.Columns), pgx.Identifier{t.Schema, t.Name}.Sanitize(), t.Predicate))
		closeErr := gz.Close()
		fileErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if fileErr != nil {
			return nil, fileErr
		}
		t.Rows = tag.RowsAffected()
		t.SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	err = os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0600)
	return m, err
}

// Verify streams compressed payloads, checks hashes, CSV row counts and widths.
func Verify(dir string) (*Manifest, error) { return verify(dir) }

// Restore creates independent, unconstrained analysis tables in a new schema.
// Source schemas must exist at the recorded migration version. CTAS preserves
// types without executing SQL types or predicates supplied by the manifest.
func Restore(ctx context.Context, conn *pgx.Conn, dir, schema string) error {
	m, err := Verify(dir)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(schema, "transit_archive_") {
		return fmt.Errorf("analysis schema must start with transit_archive_")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL TIME ZONE 'UTC'; SET LOCAL DateStyle = 'ISO, YMD'; SET LOCAL IntervalStyle = 'postgres'`); err != nil {
		return err
	}
	var version int64
	var dirty bool
	if err = tx.QueryRow(ctx, `SELECT version,dirty FROM public.schema_migrations`).Scan(&version, &dirty); err != nil {
		return err
	}
	if dirty || version != m.MigrationVersion {
		return fmt.Errorf("restore requires migration version %d", m.MigrationVersion)
	}
	if _, err = tx.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		return err
	}
	for _, t := range m.Tables {
		cols, e := columns(ctx, tx, t.Schema, t.Name)
		if e != nil {
			return e
		}
		if !sameColumns(cols, t.Columns) {
			return fmt.Errorf("schema differs for %s.%s", t.Schema, t.Name)
		}
		target := pgx.Identifier{schema, t.Schema + "__" + t.Name}.Sanitize()
		if _, err = tx.Exec(ctx, fmt.Sprintf("CREATE TABLE %s AS SELECT %s FROM %s WITH NO DATA", target, columnSQL(cols), pgx.Identifier{t.Schema, t.Name}.Sanitize())); err != nil {
			return err
		}
		f, e := os.Open(filepath.Join(dir, t.File))
		if e != nil {
			return e
		}
		gz, e := gzip.NewReader(f)
		if e != nil {
			f.Close()
			return e
		}
		tag, e := tx.Conn().PgConn().CopyFrom(ctx, gz, fmt.Sprintf("COPY %s (%s) FROM STDIN WITH (FORMAT csv, NULL '\\N')", target, columnSQL(cols)))
		gz.Close()
		f.Close()
		if e != nil {
			return e
		}
		if tag.RowsAffected() != t.Rows {
			return fmt.Errorf("restored row count mismatch: %s", t.File)
		}
	}
	return tx.Commit(ctx)
}
func sameColumns(a, b []Column) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
