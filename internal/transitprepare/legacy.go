package transitprepare

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// PrepareLegacy removes the large stop_visit rewrite and index build from
// migration 28. Existing table names and the old primary key remain available
// throughout preparation. Migration 28 performs only the final key swap.
// The caller supplies a dedicated connection: CREATE INDEX CONCURRENTLY cannot
// run inside a transaction. Interrupted preparation can safely be repeated.
func PrepareLegacy(ctx context.Context, conn *pgx.Conn) error {
	var version int
	var dirty bool
	if err := conn.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("refusing legacy preparation with dirty migration state")
	}
	if version >= 28 {
		return nil
	}
	// Metadata lock acquisition is bounded; a busy deployment retries rather than
	// placing a long exclusive-lock waiter ahead of live recorder statements.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='2s';
 ALTER TABLE transit.stop_visit ADD COLUMN IF NOT EXISTS service_date date;
 CREATE OR REPLACE FUNCTION transit.fill_stop_visit_service_date() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF NEW.service_date IS NULL THEN
   NEW.service_date:=((NEW.observed_at AT TIME ZONE 'America/Thunder_Bay')-interval '4 hours')::date;
  END IF;
  RETURN NEW;
 END $$;
 DROP TRIGGER IF EXISTS fill_service_date ON transit.stop_visit;
 DROP TRIGGER IF EXISTS a_fill_service_date ON transit.stop_visit;
 CREATE TRIGGER a_fill_service_date BEFORE INSERT OR UPDATE ON transit.stop_visit FOR EACH ROW EXECUTE FUNCTION transit.fill_stop_visit_service_date();
 CREATE TABLE IF NOT EXISTS transit.stop_visit_duplicate_archive AS SELECT * FROM transit.stop_visit WITH NO DATA;`); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// Keep the old conflict target available after migration28 replaces its
	// primary key. Older recorder binaries may still use it until handoff.
	var oldIndex string
	if err = conn.QueryRow(ctx, `SELECT pg_get_indexdef(indexrelid) FROM pg_index WHERE indrelid='transit.stop_visit'::regclass AND indisprimary`).Scan(&oldIndex); err != nil {
		return err
	}
	using := strings.Index(oldIndex, "USING ")
	if using < 0 {
		return fmt.Errorf("unexpected stop_visit primary index definition")
	}
	if err = legacyIndex(ctx, conn, "stop_visit_legacy_writer_key", `CREATE UNIQUE INDEX CONCURRENTLY stop_visit_legacy_writer_key ON transit.stop_visit `+oldIndex[using:]); err != nil {
		return err
	}
	// A partial index avoids rescanning already-backfilled rows in every batch.
	if err = legacyIndex(ctx, conn, "stop_visit_service_date_pending", `CREATE INDEX CONCURRENTLY stop_visit_service_date_pending ON transit.stop_visit(service_date) WHERE service_date IS NULL`); err != nil {
		return err
	}
	for {
		tag, err := conn.Exec(ctx, `WITH batch AS (SELECT ctid FROM transit.stop_visit WHERE service_date IS NULL LIMIT 10000)
   UPDATE transit.stop_visit v SET service_date=((observed_at AT TIME ZONE 'America/Thunder_Bay')-interval '4 hours')::date FROM batch WHERE v.ctid=batch.ctid`)
		if err != nil {
			return fmt.Errorf("backfill stop visit dates: %w", err)
		}
		if tag.RowsAffected() == 0 {
			break
		}
	}
	// Keep complete duplicate evidence atomically; only surplus same-day records
	// move, and the original primary key continues protecting live writes.
	if _, err = conn.Exec(ctx, `WITH ranked AS (
 SELECT ctid,row_number() OVER (PARTITION BY service_date,trip_id,stop_id ORDER BY observed_at,ctid) n FROM transit.stop_visit
 ), moved AS (
 DELETE FROM transit.stop_visit v USING ranked r WHERE v.ctid=r.ctid AND r.n>1 RETURNING v.*
 ) INSERT INTO transit.stop_visit_duplicate_archive SELECT * FROM moved`); err != nil {
		return fmt.Errorf("preserve duplicate stop visits: %w", err)
	}
	if err = legacyIndex(ctx, conn, "stop_visit_service_day_prepared", `CREATE UNIQUE INDEX CONCURRENTLY stop_visit_service_day_prepared ON transit.stop_visit(service_date,trip_id,stop_id)`); err != nil {
		return err
	}
	if _, err = conn.Exec(ctx, `DROP INDEX CONCURRENTLY IF EXISTS transit.stop_visit_service_date_pending`); err != nil {
		return err
	}

	// A validated check lets SET NOT NULL avoid a table scan while holding the
	// exclusive lock. Validation itself permits ordinary reads and writes.
	tx, err = conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='2s';
 DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='transit.stop_visit'::regclass AND conname='stop_visit_service_date_present') THEN
 ALTER TABLE transit.stop_visit ADD CONSTRAINT stop_visit_service_date_present CHECK(service_date IS NOT NULL) NOT VALID;
 END IF; END $$;`); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if _, err = conn.Exec(ctx, `ALTER TABLE transit.stop_visit VALIDATE CONSTRAINT stop_visit_service_date_present`); err != nil {
		return err
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='2s'; ALTER TABLE transit.stop_visit ALTER COLUMN service_date SET NOT NULL`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func legacyIndex(ctx context.Context, conn *pgx.Conn, name, create string) error {
	var exists, valid bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_index WHERE indexrelid=to_regclass($1)), COALESCE((SELECT indisvalid FROM pg_index WHERE indexrelid=to_regclass($1)),false)`, "transit."+name).Scan(&exists, &valid); err != nil {
		return err
	}
	if exists && !valid {
		if _, err := conn.Exec(ctx, "DROP INDEX CONCURRENTLY "+pgx.Identifier{"transit", name}.Sanitize()); err != nil {
			return err
		}
	}
	if !valid {
		if _, err := conn.Exec(ctx, create); err != nil {
			return fmt.Errorf("prepare %s: %w", name, err)
		}
	}
	return nil
}
