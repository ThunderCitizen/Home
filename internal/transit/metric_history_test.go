package transit

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"thundercitizen/internal/transit/chunk"
)

func metricHistoryDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `CREATE TABLE transit.stop_visit (
        trip_id text,stop_id text,route_id text,vehicle_id text,observed_at timestamptz,
        entered_at timestamptz,exited_at timestamptz,inside_polls smallint,distance_m real,
        PRIMARY KEY(trip_id,stop_id));
        ALTER TABLE transit.cancellation ADD COLUMN headsign text;
        ALTER TABLE transit.stop_delay ADD COLUMN headsign text, ADD COLUMN stop_sequence int;
        CREATE TABLE transit.vehicle_position(id bigint,feed_timestamp timestamptz,vehicle_id text,trip_id text,
        current_stop_id text,stop_status text,latitude float8,longitude float8);`)
	migration, err := os.ReadFile("../../migrations/000028_transit_metric_history.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	// The shared reader fixture already has the extended chunk columns.
	routeQueryExec(t, db, strings.Split(string(migration), "ALTER TABLE transit.route_band_chunk")[0])
	return db
}
func metricFixtureSchedule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"stops.txt":          "stop_id,stop_lat,stop_lon\nfirst,48.3809,-89.2477\na,48.3819,-89.2477\nb,48.3829,-89.2477\nlast,48.3839,-89.2477\n",
		"trips.txt":          "route_id,service_id,trip_id,direction_id\nR,W,t0,0\nR,W,t1,0\nR,W,t2,0\nR,W,t3,0\n",
		"calendar_dates.txt": "service_id,date,exception_type\nW,20260803,1\nW,20260804,1\n",
		"stop_times.txt":     "trip_id,arrival_time,departure_time,stop_id,stop_sequence,timepoint\n",
	}
	for i, id := range []string{"t0", "t1", "t2", "t3"} {
		for j, stop := range []string{"first", "a", "b", "last"} {
			at := time.Date(2026, 8, 3, 6, i*10+j*10, 0, 0, TZ).Format("15:04:05")
			files["stop_times.txt"] += id + "," + at + "," + at + "," + stop + "," + string(rune('1'+j)) + ",1\n"
		}
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func TestMetricHistoryDatabase(t *testing.T) {
	db := metricHistoryDB(t)
	ctx := t.Context()
	date := time.Date(2026, 8, 3, 0, 0, 0, 0, TZ)
	dir := metricFixtureSchedule(t)
	id, err := ImportMetricSchedule(ctx, db, dir, "fixture", date.AddDate(0, 0, -1))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ImportMetricSchedule(ctx, db, dir, "repeat", date)
	if err != nil || again != id {
		t.Fatal(again, err)
	}
	routeQueryExec(t, db, fmt.Sprintf(`ALTER TABLE transit.metric_passage ALTER COLUMN schedule_id SET DEFAULT '%s';
        ALTER TABLE transit.metric_passage ALTER COLUMN detector_version SET DEFAULT 1;`, id))
	routeQueryExec(t, db, `INSERT INTO transit.stop_delay(date,trip_id,stop_id,departure_delay,arrival_delay) VALUES
        ('2026-08-03','t0','a',-300,0),('2026-08-03','t0','b',600,0),
        ('2026-08-03','t1','a',0,0),('2026-08-03','t1','b',0,0);
        INSERT INTO transit.cancellation(trip_id,start_date,start_time,feed_timestamp) VALUES
        ('t2','20260803','06:20:00','2026-08-03 06:05-04'),('t2','20260803','06:20:00','2026-08-03 06:15-04');
        INSERT INTO transit.metric_passage(date,trip_id,stop_id,observed_at,source_a,source_b) VALUES
        ('2026-08-03','t0','a','2026-08-03 06:10-04',1,2),('2026-08-03','t0','b','2026-08-03 06:20-04',2,3),
        ('2026-08-03','t1','a','2026-08-03 06:20-04',3,4),('2026-08-03','t1','b','2026-08-03 06:30-04',4,5),
        ('2026-08-03','t3','a','2026-08-03 06:40-04',5,6),('2026-08-03','t3','b','2026-08-03 06:50-04',6,7);`)
	for range 2 {
		if n, err := BuildChunksForDate(ctx, db, date); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		rows, err := NewRepo(db).Chunks(ctx, date, date)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		c := rows[0]
		if c.OTPCount != 4 || c.OTPOnTime != 2 || c.Early != 1 || c.Late != 1 || c.Scheduled != 4 || c.Cancelled != 1 || c.NoNotice != 0 || c.EligibleWindows != 2 {
			t.Fatalf("counts: %+v", c)
		}
		ewt, ok := chunk.KPI(rows, chunk.MetricEWT, "")
		if !ok || math.Abs(ewt-200.0/60) > 1e-9 {
			t.Fatal(ewt, ok)
		}
	}
	routeQueryExec(t, db, `DELETE FROM transit.metric_passage WHERE trip_id='t1' AND stop_id='a';`)
	if _, err := BuildChunksForDate(ctx, db, date); err != nil {
		t.Fatal(err)
	}
	rows, err := NewRepo(db).Chunks(ctx, date, date)
	if err != nil || rows[0].EligibleWindows != 1 || rows[0].TotalWindows != 2 {
		t.Fatal(rows, err)
	}
	details, err := LoadCancelDetails(ctx, db, date, date)
	if err != nil || len(details) != 1 || details[0].LeadMin != 15 {
		t.Fatal(details, err)
	}
	routeQueryExec(t, db, `INSERT INTO transit.cancellation(trip_id,start_date,start_time,feed_timestamp,headsign) VALUES ('t2','20260803','06:20:00','2026-08-02 23:20-04','changed');`)
	details, err = LoadCancelDetails(ctx, db, date, date)
	if err != nil || len(details) != 1 || details[0].LeadMin != 420 {
		t.Fatal(details, err)
	}
	// Historical classification still works after the mutable schedule is empty.
	routeQueryExec(t, db, `TRUNCATE transit.trip_catalog,transit.scheduled_stop;`)
	if _, err := BuildChunksForDate(ctx, db, date); err != nil {
		t.Fatal(err)
	}
	// Export samples must reproduce the chunks even after live GTFS is replaced.
	files := bundleFiles(DateRange{From: "2026-08-03", To: "2026-08-03"})
	var exported, counted int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM ("+files[1].sql+") e WHERE delay_sec IS NOT NULL").Scan(&exported); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT sum(otp_count) FROM transit.route_band_chunk`).Scan(&counted); err != nil || exported != counted {
		t.Fatalf("exported %d samples, chunks count %d: %v", exported, counted, err)
	}
	// An incomplete-day build must be finalized after the service day closes.
	routeQueryExec(t, db, `UPDATE transit.metric_rebuild SET built_at='2026-08-03 23:55-04';`)
	dates, err := NewChunkRollup(db).findMissingDates(ctx, date, date)
	if err != nil || len(dates) != 1 {
		t.Fatal(dates, err)
	}
	// A 01:00 local build is still unfinished, even when the DB session uses UTC.
	routeQueryExec(t, db, `UPDATE transit.metric_rebuild SET built_at='2026-08-04 01:00-04';`)
	dates, err = NewChunkRollup(db).findMissingDates(ctx, date, date)
	if err != nil || len(dates) != 1 {
		t.Fatal(dates, err)
	}
	routeQueryExec(t, db, `UPDATE transit.metric_rebuild SET built_at='2026-08-04 04:01-04';`)
	dates, err = NewChunkRollup(db).findMissingDates(ctx, date, date)
	if err != nil || len(dates) != 0 {
		t.Fatal(dates, err)
	}
	// A revised archive must repair invalidated history outside the normal
	// recent-date scan, instead of leaving permanent gaps after a GTFS refresh.
	routeQueryExec(t, db, `UPDATE transit.route_band_chunk SET metric_version=0; DELETE FROM transit.metric_rebuild;`)
	dates, err = NewChunkRollup(db).findMissingDates(ctx, date.AddDate(0, 0, 60), date.AddDate(0, 0, 62))
	if err != nil || len(dates) != 1 || !dates[0].Equal(date) {
		t.Fatal(dates, err)
	}
}
func TestVisitIdentityIncludesServiceDate(t *testing.T) {
	db := metricHistoryDB(t)
	tracker := newVehicleTracker(db)
	ctx := t.Context()
	first := time.Date(2026, 8, 3, 23, 50, 0, 0, TZ)
	second := first.AddDate(0, 0, 1)
	for _, at := range []time.Time{first, first, second} {
		if err := tracker.insertStopVisitEntries(ctx, []stopVisitEntry{{tripID: "repeat", stopID: "a", routeID: "R", vehicleID: "bus", enteredAt: at}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.updateStopVisitExits(ctx, []stopVisitExit{{tripID: "repeat", stopID: "a", enteredAt: second, exitedAt: second.Add(time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	var count, exits int
	if err := db.QueryRow(ctx, `SELECT count(*),count(exited_at) FROM transit.stop_visit`).Scan(&count, &exits); err != nil || count != 2 || exits != 1 {
		t.Fatal(count, exits, err)
	}
}
func TestPassageScreening(t *testing.T) {
	aLat, aLon := offsetM(-70, 0)
	bLat, bLon := offsetM(70, 0)
	t0 := time.Date(2026, 8, 3, 6, 0, 0, 0, TZ)
	a := passageFix{ID: 1, Trip: "t", Vehicle: "v", Stop: "a", At: t0, Lat: aLat, Lon: aLon}
	b := passageFix{ID: 2, Trip: "t", Vehicle: "v", Stop: "last", At: t0.Add(15 * time.Second), Lat: bLat, Lon: bLon}
	trip := MetricTrip{ID: "t", Stops: []MetricStop{{ID: "first"}, {ID: "a", Lat: testCenterLat, Lon: testCenterLon, Timepoint: true}, {ID: "last"}}}
	got := passageBetween(a, b, trip)
	if len(got) != 1 || math.Abs(got[0].At.Sub(t0).Seconds()-7.5) > .1 {
		t.Fatal(got)
	}
	b.At = t0.Add(2 * time.Minute)
	if got := passageBetween(a, b, trip); len(got) > 0 {
		t.Fatal("interpolated across outage", got)
	}
	b.At = t0.Add(15 * time.Second)
	b.Stop = "unrelated"
	if got := passageBetween(a, b, trip); len(got) > 0 {
		t.Fatal("uncorroborated proximity", got)
	}
}
func TestMetricTimeOriginDST(t *testing.T) {
	for _, value := range []string{"2026-03-08", "2026-11-01", "2026-08-03"} {
		date, err := time.ParseInLocation("2006-01-02", value, TZ)
		if err != nil {
			t.Fatal(err)
		}
		if got := metricTimeOrigin(date).Add(6 * time.Hour).In(TZ).Hour(); got != 6 {
			t.Fatalf("%s hour=%d", value, got)
		}
	}
}

func TestMetricMigrationPreservesLocalDuplicateVisits(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `CREATE TABLE transit.stop_visit(id bigint PRIMARY KEY,trip_id text,stop_id text,observed_at timestamptz);
        INSERT INTO transit.stop_visit VALUES(1,'t','s','2026-08-03 06:00-04'),(2,'t','s','2026-08-03 06:01-04'),(3,'t','s','2026-08-04 06:00-04');
        CREATE TABLE transit.vehicle_position(id bigint,measurement_timestamp timestamptz,trip_start_date text,current_stop_sequence int);`)
	migration, err := os.ReadFile("../../migrations/000028_transit_metric_history.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	routeQueryExec(t, db, strings.Split(string(migration), "ALTER TABLE transit.route_band_chunk")[0])
	var retained, archived int
	if err := db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM transit.stop_visit),(SELECT count(*) FROM transit.stop_visit_duplicate_archive)`).Scan(&retained, &archived); err != nil || retained != 2 || archived != 1 {
		t.Fatal(retained, archived, err)
	}
	var id int
	if err := db.QueryRow(t.Context(), `SELECT id FROM transit.stop_visit_duplicate_archive`).Scan(&id); err != nil || id != 2 {
		t.Fatal(id, err)
	}
}
