package transit

import (
	"os"
	"reflect"
	"testing"
	"thundercitizen/internal/transit/chunk"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func installRouteDaySummary(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	routeQueryExec(t, db, `CREATE TABLE IF NOT EXISTS transit.metric_rebuild(date date PRIMARY KEY,version int NOT NULL,schedule_id text NOT NULL,built_at timestamptz NOT NULL DEFAULT now())`)
	sql, err := os.ReadFile("../../migrations/000029_transit_route_day_summary.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	routeQueryExec(t, db, string(sql))
}

func TestRouteDayMigrationAndRefresh(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `INSERT INTO transit.route_band_chunk
 (route_id,date,band,trip_count,scheduled_count,cancelled_count,otp_count,otp_on_time,window_seconds,wait_observed_area,wait_scheduled_area,cv_weight,cv_weighted_sum)
 VALUES ('R','2026-06-15','morning',2,5,1,2,2,60,3600,1800,60,30),
 ('R','2026-06-15','midday',8,15,1,8,0,120,14400,3600,120,30),
 ('R','2026-06-15','evening',0,100,0,0,0,0,0,0,0,0),
 ('missing','2026-06-15','morning',0,20,0,0,0,0,0,0,0,0),
 ('old','2026-06-15','morning',40,40,10,40,40,0,0,0,0,0),
 ('R','2026-06-16','morning',99,99,0,99,99,0,0,0,0,0);
 UPDATE transit.route_band_chunk SET metric_version=0 WHERE route_id='old';`)
	installRouteDaySummary(t, db) // Backfill pre-existing bands, not raw events.
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, TZ)
	summaries, err := NewRepo(db).RouteDaySummaries(t.Context(), date)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 3 {
		t.Fatalf("got %d daily tuples", len(summaries))
	}
	d := summaries["R"]
	if d.Trips != 10 || d.Scheduled != 120 || d.CancellationScheduled != 20 || d.OTPCount != 10 || d.OTPOnTime != 2 || d.IsFinal {
		t.Fatalf("daily row: %+v", d)
	}
	bands, err := NewRepo(db).RouteChunks(t.Context(), "R", date, date)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.CardStats(), RouteRowKPIFromChunks(bands, "R"); !reflect.DeepEqual(got, want) {
		t.Fatalf("daily=%+v bands=%+v", got, want)
	}
	if d.CardStats().OTP != "20" || d.CardStats().CancelPct != "10.0" {
		t.Fatalf("unweighted or unobserved denominator: %+v", d.CardStats())
	}
	for _, id := range []string{"missing", "old"} {
		if got := summaries[id].CardStats(); got.HasData || got.Cancel != "" {
			t.Fatalf("%s appeared observed: %+v", id, got)
		}
	}
	// Repair works with no raw observations and replaces, rather than adds, counts.
	routeQueryExec(t, db, `DROP TABLE transit.stop_delay,transit.cancellation; UPDATE transit.route_band_chunk SET trip_count=3 WHERE route_id='R' AND date='2026-06-15' AND band='morning'`)
	for range 2 {
		if err := RefreshRouteDaySummary(t.Context(), db, date); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err = NewRepo(db).RouteDaySummaries(t.Context(), date)
	if err != nil || summaries["R"].Trips != 11 {
		t.Fatal(summaries, err)
	}
	other, err := NewRepo(db).RouteDaySummaries(t.Context(), date.AddDate(0, 0, 1))
	if err != nil || other["R"].Trips != 99 {
		t.Fatal(other, err)
	}
	// Mixed versions invalidate the whole daily tuple rather than presenting a
	// partial blend of old and new metric recipes.
	routeQueryExec(t, db, `UPDATE transit.route_band_chunk SET metric_version=0 WHERE route_id='R' AND date='2026-06-15' AND band='morning'`)
	if err := RefreshRouteDaySummary(t.Context(), db, date); err != nil {
		t.Fatal(err)
	}
	summaries, err = NewRepo(db).RouteDaySummaries(t.Context(), date)
	if err != nil || summaries["R"].Version != 0 || summaries["R"].CardStats().HasData {
		t.Fatal(summaries, err)
	}
	routeQueryExec(t, db, `DELETE FROM transit.route_band_chunk WHERE route_id='R' AND date='2026-06-15'`)
	if err := RefreshRouteDaySummary(t.Context(), db, date); err != nil {
		t.Fatal(err)
	}
	summaries, err = NewRepo(db).RouteDaySummaries(t.Context(), date)
	if _, exists := summaries["R"]; err != nil || exists {
		t.Fatal(summaries, err)
	}
}

func TestRouteDayFinalizationBoundary(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `INSERT INTO transit.route_band_chunk(route_id,date,band,built_at) VALUES ('R','2026-11-01','morning','2026-11-01 12:00-05')`)
	installRouteDaySummary(t, db)
	routeQueryExec(t, db, `SET TIME ZONE 'UTC'; INSERT INTO transit.metric_rebuild VALUES ('2026-11-01',1,'schedule','2026-11-02 03:59:59-05')`)
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, TZ)
	if err := RefreshRouteDaySummary(t.Context(), db, date); err != nil {
		t.Fatal(err)
	}
	rows, err := NewRepo(db).RouteDaySummaries(t.Context(), date)
	if err != nil || rows["R"].IsFinal {
		t.Fatal(rows, err)
	}
	routeQueryExec(t, db, `UPDATE transit.metric_rebuild SET built_at='2026-11-02 04:00-05'`)
	if err := RefreshRouteDaySummary(t.Context(), db, date); err != nil {
		t.Fatal(err)
	}
	rows, err = NewRepo(db).RouteDaySummaries(t.Context(), date)
	if err != nil || !rows["R"].IsFinal || rows["R"].ScheduleID == nil {
		t.Fatal(rows, err)
	}
}

func TestRouteDayBuildRollsBackTogether(t *testing.T) {
	db := metricHistoryDB(t)
	date := time.Date(2026, 8, 3, 0, 0, 0, 0, TZ)
	if _, err := ImportMetricSchedule(t.Context(), db, metricFixtureSchedule(t), "fixture", date); err != nil {
		t.Fatal(err)
	}
	routeQueryExec(t, db, `INSERT INTO transit.stop_delay(date,trip_id,stop_id,departure_delay) VALUES ('2026-08-03','t0','a',0)`)
	if _, err := BuildChunksForDate(t.Context(), db, date); err != nil {
		t.Fatal(err)
	}
	routeQueryExec(t, db, `INSERT INTO transit.stop_delay(date,trip_id,stop_id,departure_delay) VALUES ('2026-08-03','t1','a',0);
 ALTER TABLE transit.route_day_summary ADD CONSTRAINT fail_summary CHECK(trip_count=1) NOT VALID`)
	if _, err := BuildChunksForDate(t.Context(), db, date); err == nil {
		t.Fatal("summary failure did not fail the whole day build")
	}
	daily, err := NewRepo(db).RouteDaySummaries(t.Context(), date)
	if err != nil || daily["R"].Trips != 1 {
		t.Fatal(daily, err)
	}
	bands, err := NewRepo(db).Chunks(t.Context(), date, date)
	if err != nil || len(bands) != 1 || bands[0].Trips != 1 {
		t.Fatal(bands, err)
	}
	routeQueryExec(t, db, `ALTER TABLE transit.route_day_summary DROP CONSTRAINT fail_summary`)
	if _, err := BuildChunksForDate(t.Context(), db, date); err != nil {
		t.Fatal(err)
	}
	daily, err = NewRepo(db).RouteDaySummaries(t.Context(), date)
	if err != nil || daily["R"].Trips != 2 {
		t.Fatal(daily, err)
	}
}

func TestTotalRouteDayStats(t *testing.T) {
	rows := map[string]RouteDaySummary{
		"A":       {Trips: 8, Cancelled: 2, CancellationScheduled: 10, Quality: chunk.Quality{Version: chunk.Version}},
		"B":       {Trips: 85, Cancelled: 5, CancellationScheduled: 90, Quality: chunk.Quality{Version: chunk.Version}},
		"missing": {Scheduled: 500, Quality: chunk.Quality{Version: chunk.Version}},
		"legacy":  {Trips: 1000, Cancelled: 100, CancellationScheduled: 1000, Quality: chunk.Quality{Version: 0}},
	}
	got := TotalRouteDayStats(rows)
	if got.Trips != "93" || got.Cancelled != "7" || got.CancelPct != "7.0%" {
		t.Fatalf("pooled totals = %+v", got)
	}
	for _, rows := range []map[string]RouteDaySummary{nil, {"missing": rows["missing"]}, {"legacy": rows["legacy"]}} {
		if got := TotalRouteDayStats(rows); got.Trips != "—" || got.Cancelled != "—" || got.CancelPct != "—" {
			t.Fatalf("missing became zero: %+v", got)
		}
	}
	got = TotalRouteDayStats(map[string]RouteDaySummary{"A": {Trips: 10, CancellationScheduled: 10, Quality: chunk.Quality{Version: chunk.Version}}})
	if got.Cancelled != "0" || got.CancelPct != "0.0%" {
		t.Fatalf("observed zero missing: %+v", got)
	}
}
