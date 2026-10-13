package transitprepare

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Only these tables preserve the legacy recorder's INSERT/conflict contract.
// Never use this path for cancellation or stop_visit: their old writers must
// stop before their constraints change. Call under WithLock.
func switchCompatibleTable(ctx context.Context, conn *pgx.Conn, name string) error {
	if name != "vehicle_position" && name != "alert" {
		return fmt.Errorf("%s requires coordinated writer handoff", name)
	}
	for {
		// Catch up without holding a source-table lock. Uncommitted events become
		// visible on a subsequent attempt; acquiring NOWAIT excludes their writers.
		if _, err := Replay(ctx, conn, 1000); err != nil {
			return err
		}
		err := switchTable(ctx, conn, name)
		if err == nil {
			return nil
		}
		var pgerr *pgconn.PgError
		if !errors.Is(err, errPendingChanges) && (!errors.As(err, &pgerr) || pgerr.Code != "55P03") {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

var errPendingChanges = errors.New("table still catching up")

func switchTable(ctx context.Context, conn *pgx.Conn, name string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='5s'`); err != nil {
		return err
	}
	// Fail immediately if an old reader/writer is active; never form a DDL queue
	// in front of HTTP. Source DROP, destination move and checkpoint are atomic.
	if _, err = tx.Exec(ctx, "LOCK TABLE "+ident("transit", name)+" IN ACCESS EXCLUSIVE MODE NOWAIT"); err != nil {
		return err
	}
	var copied, swapped, same bool
	if err = tx.QueryRow(ctx, `SELECT copied,swapped,source_oid=to_regclass('transit.'||table_name) FROM transit_prepare.state WHERE table_name=$1`, name).Scan(&copied, &swapped, &same); err != nil {
		return err
	}
	if swapped {
		return nil
	}
	if !copied || !same {
		return fmt.Errorf("%s source changed or copy is incomplete", name)
	}
	// A continuously writing feed need not leave a perfectly empty journal.
	// Drain a bounded tail under the lock; larger backlogs release the lock
	// immediately and replay outside it before retrying. Only replay this table.
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM transit_prepare.delta WHERE table_name=$1`, name).Scan(&pending); err != nil {
		return err
	}
	if pending > 100 {
		return errPendingChanges
	}
	if _, err = replayTable(ctx, tx, 100, name); err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgrelid=$1::regclass AND NOT tgisinternal AND tgname NOT IN ('transit_prepare_capture','transit_prepare_no_truncate') ORDER BY oid`, "transit."+name)
	if err != nil {
		return err
	}
	var triggers []string
	for rows.Next() {
		var def string
		if err = rows.Scan(&def); err != nil {
			rows.Close()
			return err
		}
		triggers = append(triggers, def)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `SELECT c.relname FROM pg_partition_tree($1::regclass) p JOIN pg_class c ON c.oid=p.relid ORDER BY p.level,c.relname`, "transit_prepare."+name)
	if err != nil {
		return err
	}
	var relations []string
	for rows.Next() {
		var r string
		if err = rows.Scan(&r); err != nil {
			rows.Close()
			return err
		}
		relations = append(relations, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1,'id'),greatest(pg_sequence_last_value(pg_get_serial_sequence($1,'id')::regclass),pg_sequence_last_value(pg_get_serial_sequence($2,'id')::regclass)),true)`, "transit_prepare."+name, "transit."+name); err != nil {
		return err
	}
	// RESTRICT preserves unknown external dependencies by rolling back the whole
	// handoff. No CASCADE, and no deleting batches from the still-live heap.
	if _, err = tx.Exec(ctx, "DROP TABLE "+ident("transit", name)+" RESTRICT"); err != nil {
		return err
	}
	for _, r := range relations {
		// Keep automatic maintenance disabled until final schema handoff;
		// otherwise a new leaf's vacuum can block migration 31's guards.
		if _, err = tx.Exec(ctx, "ALTER TABLE "+ident("transit_prepare", r)+" SET SCHEMA transit"); err != nil {
			return err
		}
	}
	for _, def := range triggers {
		if _, err = tx.Exec(ctx, def); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE transit_prepare.state SET swapped=true WHERE table_name=$1`, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
