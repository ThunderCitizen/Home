package transitprepare

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"thundercitizen/internal/transitarchive"
)

// BootstrapMetadata lets the read-only replacement serve report pages while
// old raw tables are still live. It creates no guards and closes no months.
func BootstrapMetadata(ctx context.Context, conn *pgx.Conn, monthCloseSQL string) error {
	end := strings.Index(monthCloseSQL, "\n-- Writers")
	if end < 0 {
		return fmt.Errorf("month-close migration metadata boundary missing")
	}
	_, err := conn.Exec(ctx, strings.Replace(monthCloseSQL[:end], "CREATE TABLE transit.archive_month", "CREATE TABLE IF NOT EXISTS transit.archive_month", 1))
	return err
}

// Cutover requires the old recorder stopped and the candidate still read-only.
// Delta catch-up happens before DDL locks. All renames, guards, and the migration
// version commit together. Failure leaves the original schema usable for retry.
func Cutover(ctx context.Context, conn *pgx.Conn, monthCloseSQL string) error {
	var version int
	var dirty bool
	if err := conn.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("database migration state is dirty")
	}
	if version >= 31 {
		return nil
	}
	if version != 29 {
		return fmt.Errorf("online cutover requires prepared schema29, got %d", version)
	}
	if _, err := conn.Exec(ctx, `ALTER TABLE transit_prepare.state ADD COLUMN IF NOT EXISTS swapped boolean NOT NULL DEFAULT false`); err != nil {
		return err
	}
	var ready int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM transit_prepare.state WHERE copied`).Scan(&ready); err != nil {
		return err
	}
	if ready != len(transitarchive.PartitionTables) {
		return fmt.Errorf("only %d raw tables copied", ready)
	}
	for {
		n, err := Replay(ctx, conn, 1000)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Avoid a waiting DDL request causing a long queue of blocked page reads.
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='5s'`); err != nil {
		return err
	}
	var names []string
	for _, table := range transitarchive.PartitionTables {
		var swapped bool
		if err := tx.QueryRow(ctx, `SELECT swapped FROM transit_prepare.state WHERE table_name=$1`, table.Name).Scan(&swapped); err != nil {
			return err
		}
		if !swapped {
			names = append(names, ident("transit", table.Name))
		}
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+strings.Join(names, ",")+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return err
	}
	var changed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM transit_prepare.state WHERE NOT swapped AND source_oid<>to_regclass('transit.'||table_name))`).Scan(&changed); err != nil {
		return err
	}
	if changed {
		return fmt.Errorf("a source table was replaced during preparation")
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM transit_prepare.delta`).Scan(&pending); err != nil {
		return err
	}
	if pending > 100 {
		return fmt.Errorf("%d changes arrived during cutover; retry after stopping writers", pending)
	}
	if _, err = replay(ctx, tx, 100); err != nil {
		return err
	}
	// Preserve non-capture application triggers. Their functions remain in transit.
	rows, err := tx.Query(ctx, `SELECT pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN transit_prepare.state s ON s.source_oid=t.tgrelid WHERE NOT s.swapped AND NOT t.tgisinternal AND t.tgname NOT IN ('transit_prepare_capture','transit_prepare_no_truncate') ORDER BY t.oid`)
	if err != nil {
		return err
	}
	var triggers []string
	for rows.Next() {
		var sql string
		if err = rows.Scan(&sql); err != nil {
			rows.Close()
			return err
		}
		triggers = append(triggers, sql)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, table := range transitarchive.PartitionTables {
		var swapped bool
		if err := tx.QueryRow(ctx, `SELECT swapped FROM transit_prepare.state WHERE table_name=$1`, table.Name).Scan(&swapped); err != nil {
			return err
		}
		if !swapped && (table.Timestamp || table.Name == "cancellation") {
			// Preserve sequence allocations too, including rolled-back/cache-reserved
			// IDs. The shadow sequence already covers copied and replayed explicit IDs.
			_, err = tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1,'id'),greatest(pg_sequence_last_value(pg_get_serial_sequence($1,'id')::regclass),pg_sequence_last_value(pg_get_serial_sequence($2,'id')::regclass)),true)`, "transit_prepare."+table.Name, "transit."+table.Name)
			if err != nil {
				return err
			}
		}
	}
	// DROP RESTRICT fails closed for unknown dependent views/FKs. The new tables
	// contain the full snapshot plus all changes; no raw evidence is discarded.
	if _, err = tx.Exec(ctx, "DROP TABLE "+strings.Join(names, ",")+" RESTRICT"); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='transit_prepare' AND c.relkind IN ('p','r') AND c.relname NOT IN ('state','delta') ORDER BY CASE WHEN c.relkind='p' THEN 0 ELSE 1 END,c.relname`)
	if err != nil {
		return err
	}
	var move []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		move = append(move, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Re-enable normal maintenance atomically with making the copies live.
	// Workers cannot observe the reset options until this transaction commits.
	if _, err = tx.Exec(ctx, `DO $$ DECLARE r record; BEGIN FOR r IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='r' AND ((n.nspname='transit_prepare' AND c.relname NOT IN ('state','delta')) OR c.oid IN (SELECT p.relid FROM transit_prepare.state s CROSS JOIN LATERAL pg_partition_tree(to_regclass('transit.'||s.table_name)) p WHERE s.swapped AND p.isleaf)) LOOP EXECUTE format('ALTER TABLE %I.%I RESET (autovacuum_enabled)',r.nspname,r.relname); END LOOP; END $$;`); err != nil {
		return err
	}
	for _, name := range move {
		if _, err = tx.Exec(ctx, "ALTER TABLE "+ident("transit_prepare", name)+" SET SCHEMA transit"); err != nil {
			return err
		}
	}
	for _, sql := range triggers {
		if _, err = tx.Exec(ctx, sql); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, strings.Replace(monthCloseSQL, "CREATE TABLE transit.archive_month", "CREATE TABLE IF NOT EXISTS transit.archive_month", 1)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE schema_migrations SET version=31,dirty=false; DROP SCHEMA transit_prepare CASCADE`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
