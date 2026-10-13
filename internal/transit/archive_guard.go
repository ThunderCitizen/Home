package transit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrArchivedMonth protects retained metrics from reconstruction after their
// source observations have been frozen for archival.
var ErrArchivedMonth = errors.New("service month is frozen for archival")

// lockMetricMonth keeps the source data stable for the entire read/rebuild.
// The archive worker takes the exclusive counterpart before freezing a month.
// A dedicated connection is required because source readers use the pool.
func lockMetricMonth(ctx context.Context, db *pgxpool.Pool, date time.Time) (func(), error) {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key := date.Year()*12 + int(date.Month())
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock_shared(23010,$1)`, key); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Conn().Close(cleanup)
		cancel()
		conn.Release()
		return nil, err
	}
	release := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT pg_advisory_unlock_shared(23010,$1)`, key); err != nil {
			// Never return a session with an unreleased advisory lock to the pool.
			_ = conn.Conn().Close(cleanup)
		}
		conn.Release()
	}
	var frozen bool
	err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM transit.archive_month WHERE month=date_trunc('month',$1::date)::date AND state IN ('frozen','uploaded','published','complete'))`, date).Scan(&frozen)
	if err != nil {
		release()
		return nil, err
	}
	if frozen {
		release()
		return nil, fmt.Errorf("%w: %s", ErrArchivedMonth, date.Format("2006-01"))
	}
	return release, nil
}
