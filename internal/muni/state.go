package muni

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LastCheckedAt returns when the upstream muni bundle was last fetched.
// Returns zero time if never checked.
func LastCheckedAt(ctx context.Context, pool *pgxpool.Pool) (time.Time, error) {
	var t time.Time
	err := pool.QueryRow(ctx,
		`SELECT last_checked_at FROM public.muni_fetch_state WHERE id = 1`,
	).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return t, err
}

// MarkChecked records that we just completed a bundle check.
func MarkChecked(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO public.muni_fetch_state (id, last_checked_at)
		 VALUES (1, now())
		 ON CONFLICT (id) DO UPDATE SET last_checked_at = EXCLUDED.last_checked_at`,
	)
	return err
}
