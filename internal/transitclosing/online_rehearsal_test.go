package transitclosing

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"thundercitizen/internal/transitarchive"
	"thundercitizen/internal/transitprepare"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type onlineLatency struct {
	Calls int     `json:"calls"`
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
	MaxMS float64 `json:"max_ms"`
}

func onlineLatencySummary(samples []time.Duration) onlineLatency {
	if len(samples) == 0 {
		return onlineLatency{}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	at := func(f float64) float64 {
		return float64(samples[int(float64(len(samples)-1)*f)]) / float64(time.Millisecond)
	}
	return onlineLatency{len(samples), at(.5), at(.95), at(.99), at(1)}
}

type onlineTrafficResult struct {
	Latency onlineLatency `json:"latency"`
	IDs     []int64       `json:"-"`
	Err     error         `json:"-"`
}

// Keep real committed insert/update/delete traffic flowing while the shadow
// copy and its cutover run. Odd iterations survive for explicit reconciliation;
// even iterations exercise deletion of records encountered during the copy.
func onlineWriteTraffic(ctx context.Context, db *pgxpool.Pool, stop <-chan struct{}, marker string) onlineTrafficResult {
	var durations []time.Duration
	var ids []int64
	result := func(err error) onlineTrafficResult {
		return onlineTrafficResult{onlineLatencySummary(durations), ids, err}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for n := 0; ; n++ {
		select {
		case <-stop:
			return result(nil)
		case <-ctx.Done():
			return result(ctx.Err())
		case <-ticker.C:
		}
		started := time.Now()
		tx, err := db.Begin(ctx)
		if err != nil {
			return result(err)
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO transit.vehicle_position(feed_timestamp,vehicle_id,latitude,longitude) VALUES('2026-09-10 12:00:00+00',$1,48.4,-89.2) RETURNING id`, marker).Scan(&id)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE transit.vehicle_position SET longitude=-89.25 WHERE id=$1 AND feed_timestamp='2026-09-10 12:00:00+00'`, id)
		}
		if err == nil && n%2 == 0 {
			_, err = tx.Exec(ctx, `DELETE FROM transit.vehicle_position WHERE id=$1 AND feed_timestamp='2026-09-10 12:00:00+00'`, id)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return result(err)
		}
		if err = tx.Commit(ctx); err != nil {
			return result(err)
		}
		durations = append(durations, time.Since(started))
		if n%2 != 0 {
			ids = append(ids, id)
		}
	}
}

func onlineReadTraffic(ctx context.Context, db *pgxpool.Pool, stop <-chan struct{}, id int64) onlineTrafficResult {
	var durations []time.Duration
	result := func(err error) onlineTrafficResult {
		return onlineTrafficResult{Latency: onlineLatencySummary(durations), Err: err}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return result(nil)
		case <-ctx.Done():
			return result(ctx.Err())
		case <-ticker.C:
		}
		started := time.Now()
		var exists bool
		err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM transit.vehicle_position WHERE id=$1)`, id).Scan(&exists)
		if err != nil {
			return result(err)
		}
		if !exists {
			return result(fmt.Errorf("baseline vehicle observation %d disappeared during migration", id))
		}
		durations = append(durations, time.Since(started))
	}
}

// Opt-in, full restored backup. It never selects the application database.
func TestOnlineBackupMigration(t *testing.T) {
	rawURL := os.Getenv("TRANSIT_ONLINE_REHEARSAL_DATABASE_URL")
	if rawURL == "" {
		t.Skip("isolated restored online rehearsal DB required")
	}
	cfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "transit_backup_rehearsal_online_") {
		t.Fatal("refusing non-rehearsal database")
	}
	root := os.Getenv("TRANSIT_BACKUP_TEST_ARTIFACTS")
	if root == "" {
		t.Fatal("artifact directory required")
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(os.MkdirAll(root, 0700))
	ctx := t.Context()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	check(err)
	defer db.Close()
	conn, err := db.Acquire(ctx)
	check(err)
	defer conn.Release()
	check(transitprepare.WithLock(ctx, conn.Conn(), func() error {
		var version int
		var dirty bool
		check(conn.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty))
		if dirty || version > 29 {
			t.Fatal("fresh clean pre-partition backup required")
		}
		started := time.Now()
		check(transitprepare.PrepareLegacy(ctx, conn.Conn()))
		t.Logf("legacy online preparation: %s", time.Since(started))
		for _, v := range []int{28, 29} {
			if v <= version {
				continue
			}
			files, err := filepath.Glob(fmt.Sprintf("../../migrations/%06d*.up.sql", v))
			check(err)
			if len(files) != 1 {
				t.Fatal("migration missing")
			}
			b, err := os.ReadFile(files[0])
			check(err)
			tx, err := conn.Begin(ctx)
			check(err)
			started := time.Now()
			_, err = tx.Exec(ctx, string(b))
			if err != nil {
				tx.Rollback(ctx)
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `UPDATE schema_migrations SET version=$1,dirty=false`, v)
			check(err)
			check(tx.Commit(ctx))
			t.Logf("metadata migration %d: %s", v, time.Since(started))
		}
		_, err = conn.Exec(ctx, `DELETE FROM transit.vehicle_position WHERE feed_timestamp='2026-09-10 12:00:00+00' AND vehicle_id LIKE 'online-rehearsal-%'`)
		check(err)
		baseline := map[string]backupFingerprint{}
		saved, readErr := os.ReadFile(filepath.Join(root, "before-online-copy.json"))
		if readErr == nil {
			check(json.Unmarshal(saved, &baseline))
			if len(baseline) != 8 {
				t.Fatal("incomplete saved baseline")
			}
		}
		for _, table := range transitarchive.PartitionTables {
			if _, ok := baseline[table.Name]; ok {
				continue
			}
			f, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit", table.Name}.Sanitize(), "TRUE")
			check(err)
			baseline[table.Name] = f
			t.Logf("baseline %s: %d rows", table.Name, f.Rows)
		}
		record := func(name string, value any) {
			b, err := json.MarshalIndent(value, "", "  ")
			check(err)
			check(os.WriteFile(filepath.Join(root, name+".json"), b, 0600))
		}
		record("before-online-copy", baseline)
		var id int64
		check(conn.QueryRow(ctx, `SELECT min(id) FROM transit.vehicle_position`).Scan(&id))
		stopRead, stopWrite := make(chan struct{}), make(chan struct{})
		readResult, writeResult := make(chan onlineTrafficResult, 1), make(chan onlineTrafficResult, 1)
		marker := "online-rehearsal-" + time.Now().UTC().Format("150405")
		go func() { readResult <- onlineReadTraffic(ctx, db, stopRead, id) }()
		go func() { writeResult <- onlineWriteTraffic(ctx, db, stopWrite, marker) }()
		diskCtx, stopDisk := context.WithCancel(ctx)
		defer stopDisk()
		diskResult := make(chan onlineDiskResult, 1)
		go func() { diskResult <- onlineDiskUsage(diskCtx, db) }()
		started = time.Now()
		err = transitprepare.PrepareIncremental(ctx, conn.Conn())
		copyTime := time.Since(started)
		close(stopWrite)
		writes := <-writeResult
		if err != nil {
			close(stopRead)
			<-readResult
			t.Fatal(err)
		}
		check(writes.Err)
		t.Logf("online copy/replay: %s, writer latency %+v", copyTime, writes.Latency)
		b, err := os.ReadFile("../../migrations/000031_transit_month_close.up.sql")
		check(err)
		check(transitprepare.BootstrapMetadata(ctx, conn.Conn(), string(b)))
		started = time.Now()
		err = transitprepare.Cutover(ctx, conn.Conn(), string(b))
		cutoverTime := time.Since(started)
		stopDisk()
		disk := <-diskResult
		check(disk.Err)
		record("disk-usage", disk.Samples)
		close(stopRead)
		reads := <-readResult
		check(err)
		check(reads.Err)
		t.Logf("cutover: %s; reader latency %+v", cutoverTime, reads.Latency)
		var survivors int
		check(conn.QueryRow(ctx, `SELECT count(*) FROM transit.vehicle_position WHERE vehicle_id=$1 AND longitude=-89.25 AND id=ANY($2)`, marker, writes.IDs).Scan(&survivors))
		if survivors != len(writes.IDs) {
			t.Fatalf("live writes lost: got %d want %d", survivors, len(writes.IDs))
		}
		_, err = conn.Exec(ctx, `DELETE FROM transit.vehicle_position WHERE vehicle_id=$1`, marker)
		check(err)
		after := map[string]backupFingerprint{}
		for _, table := range transitarchive.PartitionTables {
			f, err := backupFingerprintTable(ctx, db, pgx.Identifier{"transit", table.Name}.Sanitize(), "TRUE")
			check(err)
			after[table.Name] = f
			if f != baseline[table.Name] {
				t.Fatalf("%s evidence changed: %+v vs %+v", table.Name, f, baseline[table.Name])
			}
			t.Logf("full fingerprint preserved: %s", table.Name)
		}
		if !reflect.DeepEqual(baseline, after) {
			t.Fatal("fingerprint mismatch")
		}
		record("online-migration-result", map[string]any{"copy_seconds": copyTime.Seconds(), "cutover_seconds": cutoverTime.Seconds(), "reads": reads.Latency, "writes": writes.Latency, "surviving_concurrent_rows": survivors, "raw_tables": after})
		return nil
	}))
}

// Sampling actual database files plus allocated WAL catches both the shadow
// copy and transaction-log growth. This is cluster storage, not Go heap usage.
type onlineDiskSample struct {
	At            time.Time `json:"at"`
	DatabaseBytes int64     `json:"database_bytes"`
	WALBytes      int64     `json:"wal_bytes"`
	TempBytes     int64     `json:"temp_bytes"`
}
type onlineDiskResult struct {
	Samples []onlineDiskSample
	Err     error
}

func onlineDiskUsage(ctx context.Context, db *pgxpool.Pool) onlineDiskResult {
	result := onlineDiskResult{}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		sample := onlineDiskSample{At: time.Now()}
		err := db.QueryRow(ctx, `SELECT pg_database_size(current_database()),coalesce((SELECT sum(size) FROM pg_ls_waldir()),0)::bigint,coalesce((SELECT sum(size) FROM pg_ls_tmpdir()),0)::bigint`).Scan(&sample.DatabaseBytes, &sample.WALBytes, &sample.TempBytes)
		if err != nil {
			if ctx.Err() == nil {
				result.Err = err
			}
			return result
		}
		result.Samples = append(result.Samples, sample)
		select {
		case <-ctx.Done():
			return result
		case <-timer.C:
		}
	}
}
