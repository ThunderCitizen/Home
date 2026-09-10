package transit

import (
	"reflect"
	"testing"
	"time"
)

func TestExpectedRoutesAtStopOrdersDistinctRoutes(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `
		CREATE TABLE transit.route (
			route_id text PRIMARY KEY, color text, text_color text
		);
		CREATE TABLE transit.route_pattern (
			pattern_id text PRIMARY KEY, route_id text NOT NULL, headsign text NOT NULL
		);
		CREATE TABLE transit.route_pattern_stop (pattern_id text, stop_id text);
		ALTER TABLE transit.trip_catalog ADD COLUMN pattern_id text;

		INSERT INTO transit.route VALUES
			('10', '#ten', '#white'), ('3C', '#three', '#black'), ('Night', NULL, NULL);
		INSERT INTO transit.route_pattern VALUES
			('10-out', '10', 'Downtown'), ('3c-out', '3C', 'Airport'), ('night-out', 'Night', 'Garage');
		INSERT INTO transit.route_pattern_stop VALUES
			('10-out', 'stop'), ('3c-out', 'stop'), ('3c-out', 'stop'), ('night-out', 'stop');
		INSERT INTO transit.trip_catalog (trip_id, route_id, service_id, pattern_id) VALUES
			('trip-10', '10', 'weekday', '10-out'), ('trip-3c', '3C', 'weekday', '3c-out'),
			('trip-night', 'Night', 'weekday', 'night-out');
		INSERT INTO transit.service_calendar VALUES ('weekday', '2026-09-10');
	`)

	got, err := expectedRoutesAtStop(t.Context(), db, "stop", time.Date(2026, 9, 10, 12, 0, 0, 0, TZ))
	if err != nil {
		t.Fatal(err)
	}
	want := []ExpectedRoute{
		{RouteID: "3C", Headsign: "Airport", Color: "#three", TextColor: "#black"},
		{RouteID: "10", Headsign: "Downtown", Color: "#ten", TextColor: "#white"},
		{RouteID: "Night", Headsign: "Garage"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected routes = %#v; want %#v", got, want)
	}
}
