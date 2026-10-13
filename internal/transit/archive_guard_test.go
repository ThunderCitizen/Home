package transit

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"strings"
	"testing"
	"time"
)

func TestArchivedMonthProtectsRetainedMetrics(t *testing.T) {
	db := metricHistoryDB(t)
	date := time.Date(2026, 8, 3, 0, 0, 0, 0, TZ)
	routeQueryExec(t, db, `INSERT INTO transit.archive_month VALUES ('2026-08-01','frozen')`)
	if _, err := BuildChunksForDate(t.Context(), db, date); !errors.Is(err, ErrArchivedMonth) {
		t.Fatalf("chunk rebuild: %v", err)
	}
	if _, err := RebuildMetricPassages(t.Context(), db, date); !errors.Is(err, ErrArchivedMonth) {
		t.Fatalf("passage rebuild: %v", err)
	}
	// A new timetable cannot change the schedule interpretation of a closed month.
	if _, err := ImportMetricSchedule(t.Context(), db, metricFixtureSchedule(t), "fixture", date); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM transit.metric_service`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("closed services imported: %d", count)
	}
}

func TestMetricMonthLockExcludesFreeze(t *testing.T) {
	db := routeQueryTestDB(t)
	date := time.Date(2026, 8, 3, 0, 0, 0, 0, TZ)
	release, err := lockMetricMonth(t.Context(), db, date)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Acquire(t.Context())
	if err != nil {
		release()
		t.Fatal(err)
	}
	defer conn.Release()
	var acquired bool
	if err = conn.QueryRow(t.Context(), `SELECT pg_try_advisory_lock(23010,$1)`, 2026*12+8).Scan(&acquired); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	if acquired {
		t.Fatal("archive freeze acquired while source rebuild active")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(23010,$1)`, 2026*12+8); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_unlock(23010,$1)`, 2026*12+8); err != nil {
		t.Fatal(err)
	}
}

// Rebuilds hold a session lock on a different connection than the write
// transaction. A queued exclusive closer must not make the write's trigger
// block behind itself: fail the write promptly and let the caller retry.
func TestMetricTriggerDoesNotDeadlockQueuedCloser(t *testing.T) {
	db := metricHistoryDB(t)
	sql, err := os.ReadFile("../../migrations/000031_transit_month_close.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	function := strings.Split(strings.Split(string(sql), "CREATE FUNCTION transit.guard_archive_month()")[1], "CREATE TRIGGER")[0]
	routeQueryExec(t, db, "CREATE FUNCTION transit.guard_archive_month()"+function+`CREATE TRIGGER archive_guard BEFORE INSERT OR UPDATE OR DELETE ON transit.metric_rebuild FOR EACH ROW EXECUTE FUNCTION transit.guard_archive_month('date','date','derived');`)
	date := time.Date(2026, 8, 3, 0, 0, 0, 0, TZ)
	release, err := lockMetricMonth(t.Context(), db, date)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release() }()
	closer, err := db.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Release()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := closer.Exec(ctx, `SELECT pg_advisory_lock(23010,$1)`, 2026*12+8)
		done <- err
	}()
	// Observe the queued request, not an arbitrary scheduling delay.
	for {
		var waiting bool
		if err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=$1 AND NOT granted)`, closer.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	_, err = db.Exec(ctx, `INSERT INTO transit.metric_rebuild(date,version) VALUES ('2026-08-03',1)`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
		t.Errorf("expected retryable conflict, got %v", err)
	}
	release()
	// Prevent deferred double release after the explicit release.
	release = func() {}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = closer.Exec(t.Context(), `SELECT pg_advisory_unlock(23010,$1)`, 2026*12+8); err != nil {
		t.Fatal(err)
	}
}
