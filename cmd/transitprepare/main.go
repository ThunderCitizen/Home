// transitprepare performs restartable transit preparation before an HTTP handoff.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/transit"
	"thundercitizen/internal/transitprepare"
)

func main() {
	action := flag.String("action", "prepare", "prepare beside live tables, or cutover after stopping old writers")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *action); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, action string) error {
	if action != "prepare" && action != "cutover" {
		return fmt.Errorf("unknown action %q", action)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	return transitprepare.WithLock(ctx, conn, func() error {
		sql, err := os.ReadFile("migrations/000031_transit_month_close.up.sql")
		if err != nil {
			return err
		}
		if action == "cutover" {
			return transitprepare.Cutover(ctx, conn, string(sql))
		}
		m, err := migrate.New("file://migrations", url)
		if err != nil {
			return err
		}
		defer m.Close()
		version, dirty, err := m.Version()
		if err != nil && err != migrate.ErrNilVersion {
			return err
		}
		if dirty {
			return fmt.Errorf("dirty migration %d; refusing preparation", version)
		}
		if version < 22 {
			if err = m.Migrate(22); err != nil && err != migrate.ErrNoChange {
				return err
			}
			version = 22
		}
		if version < 28 {
			if err = transitprepare.PrepareLegacy(ctx, conn); err != nil {
				return err
			}
		}
		if version < 29 {
			if err = transitprepare.ApplyMetadata(ctx, conn, "migrations"); err != nil {
				return err
			}
		}
		// Existing partitioned installations only need the small journal migration.
		if version >= 31 {
			if err = m.Up(); err != nil && err != migrate.ErrNoChange {
				return err
			}
		}
		if version == 30 {
			if err = m.Migrate(31); err != nil && err != migrate.ErrNoChange {
				return err
			}
		}
		if err = transitprepare.BootstrapMetadata(ctx, conn, string(sql)); err != nil {
			return err
		}
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			return err
		}
		defer pool.Close()
		if err = transit.ImportHistoricalSchedules(ctx, pool); err != nil {
			return err
		}
		return transitprepare.PrepareIncremental(ctx, conn)
	})
}
