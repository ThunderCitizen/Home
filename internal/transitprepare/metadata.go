package transitprepare

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ApplyMetadata applies the prepared 28/29 changes without queuing an exclusive
// lock behind a live query. Both DDL and migration version commit together, so
// a busy-table retry never leaves a spurious dirty migration marker.
func ApplyMetadata(ctx context.Context, conn *pgx.Conn, dir string) error {
	for _, version := range []int{28, 29} {
		var current int
		var dirty bool
		if err := conn.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&current, &dirty); err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("database migration state is dirty")
		}
		if current >= version {
			continue
		}
		if current != 22 && current != 28 {
			return fmt.Errorf("unexpected preparation schema %d", current)
		}
		files, err := filepath.Glob(filepath.Join(dir, fmt.Sprintf("%06d*.up.sql", version)))
		if err != nil {
			return err
		}
		if len(files) != 1 {
			return fmt.Errorf("expected one migration %d", version)
		}
		sql, err := os.ReadFile(files[0])
		if err != nil {
			return err
		}
		for {
			err = applyMetadata(ctx, conn, string(sql), current, version)
			if err == nil {
				log.Info("preparation metadata applied", "version", version)
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
func applyMetadata(ctx context.Context, conn *pgx.Conn, sql string, from, to int) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='100ms';SET LOCAL statement_timeout='5s';LOCK TABLE transit.stop_visit,transit.vehicle_position,transit.route_band_chunk,transit.stop_delay,transit.cancellation IN ACCESS EXCLUSIVE MODE NOWAIT`); err != nil {
		return err
	}
	var version int
	var dirty bool
	if err = tx.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations FOR UPDATE`).Scan(&version, &dirty); err != nil {
		return err
	}
	if dirty || version != from {
		return fmt.Errorf("migration state changed during preparation")
	}
	if _, err = tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE schema_migrations SET version=$1,dirty=false`, to); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
