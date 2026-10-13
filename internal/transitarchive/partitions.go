package transitarchive

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PartitionTable identifies raw tables whose operating-month partitions may be
// removed after verified archival. Reference tables and summaries never appear.
type PartitionTable struct {
	Name, Key string
	Timestamp bool
}

var PartitionTables = []PartitionTable{
	{"vehicle_position", "feed_timestamp", true}, {"alert", "feed_timestamp", true},
	{"stop_delay", "date", false}, {"vehicle_assignment", "date", false},
	{"metric_passage", "date", false}, {"stop_visit", "service_date", false}, {"stop_visit_duplicate_archive", "service_date", false}, {"cancellation", "start_date", false},
}

func partitionBounds(table PartitionTable, month time.Time) (string, string) {
	end := month.AddDate(0, 1, 0)
	if table.Timestamp {
		loc, _ := time.LoadLocation("America/Thunder_Bay")
		bound := func(t time.Time) string {
			return time.Date(t.Year(), t.Month(), 1, 4, 0, 0, 0, loc).Format(time.RFC3339)
		}
		return bound(month), bound(end)
	}
	format := "2006-01-02"
	if table.Key == "start_date" {
		format = "20060102"
	}
	return month.Format(format), end.Format(format)
}

// EnsurePartitions provisions the current and next operating months. Fallback
// records are moved atomically into newly created partitions. Closed-month
// writes are rejected by the archive journal trigger, including on fallbacks.
func EnsurePartitions(ctx context.Context, conn *pgx.Conn, now time.Time) error {
	loc, err := time.LoadLocation("America/Thunder_Bay")
	if err != nil {
		return err
	}
	local := now.In(loc)
	if local.Hour() < 4 {
		local = local.AddDate(0, 0, -1)
	}
	month := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return ensureMonthPartitions(ctx, conn, []time.Time{month, month.AddDate(0, 1, 0)})
}

// EnsureMonthPartitions also provisions historical months before finalization.
// Existing frozen records are relocated structurally, without rewriting through
// the parent's archive guards. All movement and attachment is atomic.
func EnsureMonthPartitions(ctx context.Context, conn *pgx.Conn, month time.Time) error {
	return ensureMonthPartitions(ctx, conn, []time.Time{month})
}

func ensureMonthPartitions(ctx context.Context, conn *pgx.Conn, months []time.Time) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Provisioning takes parent-table DDL locks. A busy reader should defer
	// this retryable operation instead of indefinitely queuing live ingestion.
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(23011,0)`); err != nil {
		return err
	}
	for _, table := range PartitionTables {
		for _, m := range months {
			lo, hi := partitionBounds(table, m)
			name := table.Name + "_p" + m.Format("200601")
			parent := pgx.Identifier{"transit", table.Name}.Sanitize()
			fallback := pgx.Identifier{"transit", table.Name + "_unassigned"}.Sanitize()
			child := pgx.Identifier{"transit", name}.Sanitize()
			var exists bool
			if err = tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "transit."+name).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				if _, err = tx.Exec(ctx, "LOCK TABLE "+parent+" IN ACCESS EXCLUSIVE MODE"); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, "ALTER TABLE "+parent+" DETACH PARTITION "+fallback); err != nil {
					return err
				}
				// Detaching removes the inherited archive guard. Copy into an
				// unattached child so frozen records cannot be rejected or skipped.
				query := fmt.Sprintf("CREATE TABLE %s (LIKE %s INCLUDING ALL)", child, parent)
				if _, err = tx.Exec(ctx, query); err != nil {
					return fmt.Errorf("provision %s: %w", name, err)
				}
				var columns string
				if err = tx.QueryRow(ctx, `SELECT string_agg(quote_ident(attname),',' ORDER BY attnum) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped AND attgenerated=''`, parent).Scan(&columns); err != nil {
					return err
				}
				pred := fmt.Sprintf("%s >= '%s' AND %s < '%s'", pgx.Identifier{table.Key}.Sanitize(), lo, pgx.Identifier{table.Key}.Sanitize(), hi)
				if _, err = tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (%s) OVERRIDING SYSTEM VALUE SELECT %s FROM %s WHERE %s", child, columns, columns, fallback, pred)); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, "DELETE FROM "+fallback+" WHERE "+pred); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ATTACH PARTITION %s FOR VALUES FROM ('%s') TO ('%s')", parent, child, lo, hi)); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, "ALTER TABLE "+parent+" ATTACH PARTITION "+fallback+" DEFAULT"); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, `INSERT INTO transit.raw_partition(table_name,month,partition_name) VALUES($1,$2::date,$3) ON CONFLICT DO NOTHING`, table.Name, m.Format("2006-01-02"), name); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// DropMonthPartitions must share the transaction that marks the journal complete.
// It never touches default quarantine partitions or nonpartitioned raw tables.
func DropMonthPartitions(ctx context.Context, tx pgx.Tx, month string) error {
	m, err := time.Parse("2006-01", month)
	if err != nil {
		return err
	}
	var verified bool
	err = tx.QueryRow(ctx, `SELECT state='published' AND raw_manifest IS NOT NULL AND raw_verified_at IS NOT NULL FROM transit.archive_month WHERE month=$1::date FOR UPDATE`, m.Format("2006-01-02")).Scan(&verified)
	if err != nil {
		return fmt.Errorf("archive journal: %w", err)
	}
	if !verified {
		return fmt.Errorf("month %s is not verified and published", month)
	}
	for _, table := range PartitionTables {
		name := table.Name + "_p" + m.Format("200601")
		lo, hi := partitionBounds(table, m)
		var residual bool
		query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE %s >= '%s' AND %s < '%s')", pgx.Identifier{"transit", table.Name + "_unassigned"}.Sanitize(), pgx.Identifier{table.Key}.Sanitize(), lo, pgx.Identifier{table.Key}.Sanitize(), hi)
		if err = tx.QueryRow(ctx, query).Scan(&residual); err != nil {
			return err
		}
		if residual {
			return fmt.Errorf("unassigned %s records belong to %s; refusing eviction", table.Name, month)
		}

		var registered bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM transit.raw_partition WHERE table_name=$1 AND month=$2::date)`, table.Name, m.Format("2006-01-02")).Scan(&registered); err != nil {
			return err
		}
		if !registered {
			continue
		}
		var attached bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_inherits WHERE inhparent=to_regclass($1) AND inhrelid=to_regclass($2))`, "transit."+table.Name, "transit."+name).Scan(&attached)
		if err != nil {
			return err
		}
		if !attached {
			return fmt.Errorf("expected attached partition %s missing; refusing partial eviction", name)
		}
		if _, err = tx.Exec(ctx, "DROP TABLE "+pgx.Identifier{"transit", name}.Sanitize()+" RESTRICT"); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM transit.raw_partition WHERE month=$1::date`, m.Format("2006-01-02"))
	return err
}

// PartitionMonths returns registered operating months, oldest first. The caller
// compares them with the current operating month before closing one.
func PartitionMonths(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT DISTINCT to_char(month,'YYYY-MM') FROM transit.raw_partition ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var months []string
	for rows.Next() {
		var month string
		if err = rows.Scan(&month); err != nil {
			return nil, err
		}
		months = append(months, month)
	}
	return months, rows.Err()
}

// PartitionPredicate is the exact set of raw rows owned by an operating month.
func PartitionPredicate(table PartitionTable, month time.Time) string {
	lo, hi := partitionBounds(table, month)
	return fmt.Sprintf("%s >= '%s' AND %s < '%s'", pgx.Identifier{table.Key}.Sanitize(), lo, pgx.Identifier{table.Key}.Sanitize(), hi)
}
