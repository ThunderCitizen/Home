package transitclosing

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"thundercitizen/internal/transit"
	"time"
)

func TestClosingVerifiedRawCheckpointSurvivesReportFailure(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	store := &memoryStore{}
	privateCalls := 0
	failReport := true
	expected := map[string][32]byte{}
	store.hook = func(key string) error {
		if !store.public[key] {
			privateCalls++
			return nil
		}
		name := key[strings.LastIndex(key, "/")+1:]
		hash := sha256.Sum256(store.files[key])
		if failReport {
			expected[name] = hash
		} else if hash != expected[name] {
			return fmt.Errorf("report changed on retry: %s", name)
		}
		if failReport && name == transit.ReportBundleFilename() {
			return fmt.Errorf("injected bundle failure")
		}
		return nil
	}
	w := New(db, store)
	w.finalize = summaryFinalizer(db)
	if err := w.RunOnce(ctx, septemberBoundary()); err == nil || !strings.Contains(err.Error(), "injected bundle failure") {
		t.Fatalf("expected report failure: %v", err)
	}
	var prefix string
	var verified time.Time
	var reportsAbsent bool
	if err := db.QueryRow(ctx, `SELECT raw_prefix,raw_verified_at,report_base_url IS NULL AND report_manifest IS NULL FROM transit.archive_month WHERE month='2026-08-01'`).Scan(&prefix, &verified, &reportsAbsent); err != nil {
		t.Fatal(err)
	}
	if journalState(t, db) != "frozen" || !reportsAbsent || privateCalls == 0 || len(expected) != 1 {
		t.Fatal("missing raw checkpoint or premature report publication")
	}
	calls := privateCalls
	failReport = false
	// A fresh worker has no in-memory knowledge of the successful raw uploads.
	retry := New(db, store)
	retry.finalize = func(context.Context, time.Time) error { return fmt.Errorf("unexpected repeat finalization") }
	if err := retry.RunOnce(ctx, septemberBoundary()); err != nil {
		t.Fatal(err)
	}
	if privateCalls != calls {
		t.Fatalf("reuploaded private raw data: %d -> %d", calls, privateCalls)
	}
	var gotPrefix string
	var gotVerified time.Time
	if err := db.QueryRow(ctx, `SELECT raw_prefix,raw_verified_at FROM transit.archive_month WHERE month='2026-08-01'`).Scan(&gotPrefix, &gotVerified); err != nil {
		t.Fatal(err)
	}
	if prefix != gotPrefix || !verified.Equal(gotVerified) {
		t.Fatal("verified raw checkpoint changed")
	}
	if journalState(t, db) != "complete" {
		t.Fatal("retry did not complete eviction")
	}
	var remaining int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM transit.vehicle_position WHERE feed_timestamp < '2026-09-01 04:00-04'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("raw eviction: rows=%d error=%v", remaining, err)
	}
}

func TestClosingRawFailureHasNoCheckpoint(t *testing.T) {
	db := closingDB(t)
	ctx := context.Background()
	store := &memoryStore{fail: true}
	w := New(db, store)
	w.finalize = summaryFinalizer(db)
	if err := w.RunOnce(ctx, septemberBoundary()); err == nil {
		t.Fatal("expected raw upload failure")
	}
	var absent bool
	if err := db.QueryRow(ctx, `SELECT raw_prefix IS NULL AND raw_manifest IS NULL AND raw_verified_at IS NULL FROM transit.archive_month WHERE month='2026-08-01'`).Scan(&absent); err != nil {
		t.Fatal(err)
	}
	if !absent || journalState(t, db) != "frozen" {
		t.Fatal("failed raw upload was checkpointed")
	}
	// Incomplete or malformed persisted evidence must not be trusted on retry.
	for _, tc := range []struct{ sql, want string }{
		{`UPDATE transit.archive_month SET raw_prefix='https://example.test/raw'`, "incomplete verified raw checkpoint"},
		{`UPDATE transit.archive_month SET raw_manifest='{}',raw_verified_at=now()`, "invalid verified raw checkpoint release"},
	} {
		closingExec(t, db, tc.sql)
		calls := store.calls
		if err := New(db, store).RunOnce(ctx, septemberBoundary()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("expected %s: %v", tc.want, err)
		}
		if store.calls != calls {
			t.Fatal("attempted upload with invalid checkpoint")
		}
	}
}
