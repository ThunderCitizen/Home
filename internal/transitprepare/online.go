// Package transitprepare performs the expensive partition copy beside live tables.
// Committed row changes are journalled until a bounded table handoff.
package transitprepare

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"thundercitizen/internal/logger"
	"thundercitizen/internal/transitarchive"
)

//go:embed shadow.sql
var shadowSQL string

var log = logger.New("transit_prepare")

func ident(schema, name string) string { return pgx.Identifier{schema, name}.Sanitize() }

// Prepare copies each table in its own repeatable-read snapshot. A failed copy
// rolls back that table only; completed tables and their deltas survive restart.
// Callers must serialize Prepare/Cutover using WithLock.
func Prepare(ctx context.Context, conn *pgx.Conn) error {
	return prepare(ctx, conn, false)
}

// PrepareIncremental releases each legacy-compatible large table immediately
// after copying it. Other tables retain the coordinated writer handoff.
func PrepareIncremental(ctx context.Context, conn *pgx.Conn) error {
	return prepare(ctx, conn, true)
}

func prepare(ctx context.Context, conn *pgx.Conn, incremental bool) error {
	var version int
	var dirty bool
	if err := conn.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("database migration state is dirty")
	}
	if version >= 30 {
		return nil
	}
	if version != 29 {
		return fmt.Errorf("online partition preparation requires schema 29, got %d", version)
	}
	var ready bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('transit_prepare.state') IS NOT NULL`).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `CREATE SCHEMA transit_prepare; CREATE TABLE transit_prepare.state(table_name text PRIMARY KEY,source_oid oid NOT NULL,snapshot pg_snapshot,copied boolean NOT NULL DEFAULT false); CREATE TABLE transit_prepare.delta(seq bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,table_name text NOT NULL,xid xid8 NOT NULL,old_row jsonb,new_row jsonb); CREATE INDEX ON transit_prepare.delta(table_name,seq);
CREATE FUNCTION transit_prepare.capture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'TRUNCATE is disabled during online partition preparation'; END IF;
 INSERT INTO transit_prepare.delta(table_name,xid,old_row,new_row) VALUES(TG_TABLE_NAME,pg_current_xact_id(),CASE WHEN TG_OP IN ('UPDATE','DELETE') THEN to_jsonb(OLD) END,CASE WHEN TG_OP IN ('INSERT','UPDATE') THEN to_jsonb(NEW) END);
 RETURN NULL;
END $$;`); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, shadowSQL); err != nil {
			return fmt.Errorf("create shadow partitions: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	if _, err := conn.Exec(ctx, `ALTER TABLE transit_prepare.state ADD COLUMN IF NOT EXISTS swapped boolean NOT NULL DEFAULT false`); err != nil {
		return err
	}
	if err := ensureStagingMaintenance(ctx, conn); err != nil {
		return err
	}
	for _, table := range transitarchive.PartitionTables {
		var installed bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM transit_prepare.state WHERE table_name=$1)`, table.Name).Scan(&installed); err != nil {
			return err
		}
		if !installed {
			for {
				err := installCapture(ctx, conn, table.Name)
				if err == nil {
					break
				}
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
					return fmt.Errorf("install capture for %s: %w", table.Name, err)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
	}
	for _, table := range transitarchive.PartitionTables {
		var copied, swapped bool
		if err := conn.QueryRow(ctx, `SELECT copied,swapped FROM transit_prepare.state WHERE table_name=$1`, table.Name).Scan(&copied, &swapped); err != nil {
			return err
		}
		if swapped {
			continue
		}
		if !copied {
			// A failed INSERT transaction can leave allocated heap/index pages.
			// Commit this truncation separately so retries reclaim those files
			// before starting another copy. This table has no completed snapshot.
			if _, err := conn.Exec(ctx, "TRUNCATE "+ident("transit_prepare", table.Name)); err != nil {
				return err
			}
			began := time.Now()
			log.Info("copying raw table beside live data", "table", table.Name)
			if err := copyTable(ctx, conn, table.Name); err != nil {
				return err
			}
			log.Info("raw table copy complete", "table", table.Name, "elapsed", time.Since(began))
		}
		if incremental && (table.Name == "vehicle_position" || table.Name == "alert") {
			if _, err := conn.Exec(ctx, "ANALYZE "+ident("transit_prepare", table.Name)); err != nil {
				return err
			}
			if err := switchCompatibleTable(ctx, conn, table.Name); err != nil {
				return err
			}
			log.Info("raw table switched; original storage released", "table", table.Name)
		}
	}
	// Sample the completed copies while the original app still serves, keeping
	// the expensive statistics work out of the final cutover transaction.
	for _, table := range transitarchive.PartitionTables {
		var swapped bool
		if err := conn.QueryRow(ctx, `SELECT swapped FROM transit_prepare.state WHERE table_name=$1`, table.Name).Scan(&swapped); err != nil {
			return err
		}
		schema := "transit_prepare"
		if swapped {
			schema = "transit"
		}
		if _, err := conn.Exec(ctx, "ANALYZE "+ident(schema, table.Name)); err != nil {
			return err
		}
	}
	// Bound preparation's tail even when ingestion is busy. Final drain runs
	// after the old recorder stops and before acquiring any table DDL locks.
	for i := 0; i < 100; i++ {
		n, err := Replay(ctx, conn, 1000)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
	return nil
}

func columns(ctx context.Context, conn interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, schema, name string) (string, error) {
	var cols string
	err := conn.QueryRow(ctx, `SELECT string_agg(quote_ident(attname),',' ORDER BY attnum) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped AND attgenerated=''`, schema+"."+name).Scan(&cols)
	return cols, err
}
func copyTable(ctx context.Context, conn *pgx.Conn, name string) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE transit_prepare.state SET snapshot=pg_current_snapshot() WHERE table_name=$1`, name); err != nil {
		return err
	}
	cols, err := columns(ctx, tx, "transit_prepare", name)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (%s) OVERRIDING SYSTEM VALUE SELECT %s FROM %s`, ident("transit_prepare", name), cols, cols, ident("transit", name))); err != nil {
		return fmt.Errorf("copy %s: %w", name, err)
	}
	if name == "vehicle_position" || name == "alert" || name == "cancellation" {
		if _, err = tx.Exec(ctx, fmt.Sprintf("SELECT setval(pg_get_serial_sequence('%s','id'), greatest(coalesce(max(id),1),1),true) FROM %s", "transit_prepare."+name, ident("transit_prepare", name))); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE transit_prepare.state SET copied=true WHERE table_name=$1`, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type delta struct {
	seq      int64
	table    string
	old, new json.RawMessage
	visible  bool
}

// Replay applies committed changes in event order. Snapshot-visible changes are
// already in the bulk copy, including transactions committed before copying.
// Transactions still in progress have invisible journal rows and are picked up
// by a later batch. Deleting journal rows shares the replay transaction.
func Replay(ctx context.Context, conn *pgx.Conn, limit int) (int, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	n, err := replay(ctx, tx, limit)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}
func replay(ctx context.Context, tx pgx.Tx, limit int) (int, error) {
	return replayTable(ctx, tx, limit, "")
}

func replayTable(ctx context.Context, tx pgx.Tx, limit int, table string) (int, error) {
	rows, err := tx.Query(ctx, `SELECT d.seq,d.table_name,d.old_row,d.new_row,pg_visible_in_snapshot(d.xid,s.snapshot) FROM transit_prepare.delta d JOIN transit_prepare.state s USING(table_name) WHERE s.copied AND ($2='' OR d.table_name=$2) ORDER BY d.seq LIMIT $1`, limit, table)
	if err != nil {
		return 0, err
	}
	var events []delta
	for rows.Next() {
		var d delta
		if err = rows.Scan(&d.seq, &d.table, &d.old, &d.new, &d.visible); err != nil {
			rows.Close()
			return 0, err
		}
		events = append(events, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, d := range events {
		if !d.visible {
			target := ident("transit_prepare", d.table)
			cols, err := columns(ctx, tx, "transit_prepare", d.table)
			if err != nil {
				return 0, err
			}
			if len(d.old) > 0 {
				var keys []string
				if err = tx.QueryRow(ctx, `SELECT coalesce(array_agg(a.attname ORDER BY k.ordinality),'{}'::text[]) FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(num,ordinality) JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.num WHERE i.indrelid=$1::regclass AND i.indisprimary`, "transit."+d.table).Scan(&keys); err != nil {
					return 0, err
				}
				pred := `to_jsonb(t)=$1::jsonb`
				if len(keys) > 0 {
					var parts []string
					for _, key := range keys {
						q := pgx.Identifier{key}.Sanitize()
						parts = append(parts, "t."+q+"=r."+q)
					}
					pred = strings.Join(parts, " AND ") + " AND to_jsonb(t)=$1::jsonb"
				}
				// Include the partition key so point updates prune unrelated months.
				for _, table := range transitarchive.PartitionTables {
					if table.Name == d.table {
						q := pgx.Identifier{table.Key}.Sanitize()
						var row map[string]json.RawMessage
						if err := json.Unmarshal(d.old, &row); err != nil {
							return 0, err
						}
						if value := row[table.Key]; len(value) > 0 && string(value) != "null" {
							pred += " AND t." + q + "=r." + q
						}
					}
				}
				// Find the physical tuple first. A parent DELETE joined on
				// tableoid/ctid can otherwise scan every partition per event.
				var child, tid string
				if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT t.tableoid::regclass::text,t.ctid::text FROM %s t CROSS JOIN jsonb_populate_record(NULL::%s,$1::jsonb) r WHERE %s LIMIT 1`, target, target, pred), d.old).Scan(&child, &tid); err != nil {
					return 0, fmt.Errorf("delta %d locate original %s: %w", d.seq, d.table, err)
				}
				result, err := tx.Exec(ctx, "DELETE FROM "+child+" WHERE ctid=$1::tid", tid)
				if err != nil {
					return 0, err
				}
				if result.RowsAffected() != 1 {
					return 0, fmt.Errorf("delta %d: missing original row in %s", d.seq, d.table)
				}
			}
			if len(d.new) > 0 {
				if _, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (%s) OVERRIDING SYSTEM VALUE SELECT %s FROM jsonb_populate_record(NULL::%s,$1::jsonb)`, target, cols, cols, target), d.new); err != nil {
					return 0, fmt.Errorf("delta %d: %w", d.seq, err)
				}
			}
			if len(d.new) > 0 && (d.table == "vehicle_position" || d.table == "alert" || d.table == "cancellation") {
				if _, err = tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1,'id'),greatest(pg_sequence_last_value(pg_get_serial_sequence($1,'id')::regclass),($2::jsonb->>'id')::bigint),true)`, "transit_prepare."+d.table, d.new); err != nil {
					return 0, err
				}
			}
		}
	}
	if len(events) > 0 {
		ids := make([]int64, len(events))
		for i, d := range events {
			ids[i] = d.seq
		}
		if _, err = tx.Exec(ctx, `DELETE FROM transit_prepare.delta WHERE seq=ANY($1)`, ids); err != nil {
			return 0, err
		}
	}
	return len(events), nil
}

// WithLock prevents simultaneous preparers across separate deploy invocations.
func WithLock(ctx context.Context, conn *pgx.Conn, fn func() error) error {
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(23013,0)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("another transit preparation is running")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn.Exec(c, `SELECT pg_advisory_unlock(23013,0)`)
	}()
	return fn()
}

func installCapture(ctx context.Context, conn *pgx.Conn, name string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='5s'`); err != nil {
		return err
	}
	// NOWAIT leaves existing readers/writers and maintenance unqueued.
	if _, err = tx.Exec(ctx, "LOCK TABLE "+ident("transit", name)+" IN SHARE ROW EXCLUSIVE MODE NOWAIT"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER transit_prepare_capture AFTER INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION transit_prepare.capture(); CREATE TRIGGER transit_prepare_no_truncate BEFORE TRUNCATE ON %s FOR EACH STATEMENT EXECUTE FUNCTION transit_prepare.capture()`, ident("transit", name), ident("transit", name))); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO transit_prepare.state(table_name,source_oid) VALUES($1,$2::regclass)`, name, "transit."+name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Reconcile options on resume too (e.g. preparation began with an older binary).
// Never stop the serving recorder just to wait for private-table maintenance.
func ensureStagingMaintenance(ctx context.Context, conn *pgx.Conn) error {
	rows, err := conn.Query(ctx, `SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='r' AND NOT coalesce('autovacuum_enabled=false'=ANY(c.reloptions),false) AND (n.nspname='transit_prepare' OR c.oid IN (SELECT p.relid FROM transit_prepare.state s CROSS JOIN LATERAL pg_partition_tree(to_regclass('transit.'||s.table_name)) p WHERE s.swapped AND p.isleaf))`)
	if err != nil {
		return err
	}
	type relation struct{ schema, name string }
	var names []relation
	for rows.Next() {
		var name relation
		if err = rows.Scan(&name.schema, &name.name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		for {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "LOCK TABLE "+ident(name.schema, name.name)+" IN ACCESS EXCLUSIVE MODE NOWAIT")
			if err == nil {
				_, err = tx.Exec(ctx, "ALTER TABLE "+ident(name.schema, name.name)+" SET (autovacuum_enabled=false)")
			}
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				tx.Rollback(ctx)
			}
			if err == nil {
				break
			}
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}
