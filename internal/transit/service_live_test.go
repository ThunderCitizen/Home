package transit

import (
	"context"
	"errors"
	"testing"
)

func TestLiveReadsDatabaseChanges(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `
		ALTER TABLE transit.trip_catalog ADD COLUMN block_id text;
		CREATE TABLE transit.route (route_id text PRIMARY KEY);
		CREATE TABLE transit.vehicle (vehicle_id text PRIMARY KEY);
		CREATE TABLE transit.alert (
			alert_id text, feed_timestamp timestamptz, cause text, effect text,
			header text, description text, severity_level text,
			affected_routes text[], affected_stops text[],
			active_start timestamptz, active_end timestamptz
		);
		INSERT INTO transit.route VALUES ('3C'), ('no-service');
		INSERT INTO transit.vehicle VALUES ('one');
		INSERT INTO transit.trip_catalog VALUES
			('trip', '3C', 'today', 'Terminal', '23:50:00', '24:10:00', 'block');
	`)
	date := ServiceDate()
	if _, err := db.Exec(t.Context(), `INSERT INTO transit.service_calendar VALUES ('today', $1)`, date); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, nil)
	read := func() *liveData {
		t.Helper()
		live, err := svc.Live(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return live
	}
	first := read()
	if first.dashboard.FleetSize != 1 || len(first.dashboard.CancelledTrips) != 0 || len(first.noService) != 1 || first.noService[0] != "no-service" {
		t.Fatalf("unexpected initial dashboard: %+v", first)
	}
	routeQueryExec(t, db, `
		INSERT INTO transit.vehicle VALUES ('two');
		INSERT INTO transit.alert (alert_id, feed_timestamp, header) VALUES ('alert', NOW(), 'New alert');
	`)
	if _, err := db.Exec(t.Context(), `
		INSERT INTO transit.cancellation (trip_id, route_id, start_date, feed_timestamp) VALUES
			('trip', '3C', $1, NOW() - INTERVAL '2 minutes'),
			('trip', '3C', $1, NOW() - INTERVAL '1 minute'),
			('trip', '3C', $2, NOW() - INTERVAL '1 day')
	`, date.Format("20060102"), date.AddDate(0, 0, -1).Format("20060102")); err != nil {
		t.Fatal(err)
	}
	second := read()
	trips := second.dashboard.CancelledTrips["3C"]
	if second.dashboard.FleetSize != 2 || len(second.dashboard.Alerts) != 1 || len(trips) != 1 || trips[0].SnapshotCount != 2 {
		t.Fatalf("next read missed changed fleet, alerts, or cancellations: %+v; trips=%+v", second.dashboard, trips)
	}
	if len(second.incidents) != 1 || second.incidents[0].BlockID != "block" || len(second.incidents[0].Trips) != 1 {
		t.Fatalf("duplicate snapshots changed incidents: %+v", second.incidents)
	}
	routeQueryExec(t, db, `DELETE FROM transit.cancellation; DELETE FROM transit.alert;`)
	third := read()
	if len(third.dashboard.CancelledTrips) != 0 || len(third.incidents) != 0 || len(third.dashboard.Alerts) != 0 {
		t.Fatalf("removed data survived the next read: %+v", third)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if live, err := svc.Live(ctx); live != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %+v, %v", live, err)
	}
	routeQueryExec(t, db, `DROP TABLE transit.service_calendar;`)
	if live, err := svc.Live(t.Context()); live != nil || err == nil {
		t.Fatalf("database failure returned an old dashboard: %+v, %v", live, err)
	}
}
