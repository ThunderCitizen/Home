package transit

import (
	"reflect"
	"testing"
)

func TestStopAnalyticsDatabase(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `
		ALTER TABLE transit.stop ADD COLUMN latitude float8, ADD COLUMN longitude float8;
		CREATE TABLE transit.stop_visit (trip_id text, stop_id text, route_id text, observed_at timestamptz);
		CREATE TABLE transit.route_pattern (pattern_id text, route_id text);
		CREATE TABLE transit.route_pattern_stop (pattern_id text, stop_id text, is_timepoint boolean);
		INSERT INTO transit.stop (stop_id, name, latitude, longitude) VALUES
			('busy', 'Busy Stop', 48, -89),
			('ordinary', 'Ordinary Stop', 48, -89),
			('boundary', 'Boundary Stop', 48, -89),
			('single', NULL, 48, -89),
			('old-only', 'Old Stop', 48, -89),
			('unvisited', 'Unvisited Stop', 48, -89),
			('no-latitude', 'Missing Latitude', NULL, -89),
			('no-longitude', 'Missing Longitude', 48, NULL);
		INSERT INTO transit.route_pattern VALUES ('one', '1'), ('two', '2');
		INSERT INTO transit.route_pattern_stop VALUES
			('one', 'busy', true), ('two', 'busy', true),
			('one', 'single', true), ('one', 'boundary', true), ('one', 'ordinary', false);
		INSERT INTO transit.stop_visit VALUES
			('old-busy', 'busy', '9', CURRENT_DATE - INTERVAL '10 days'),
			('two-a', 'busy', '2', CURRENT_DATE - INTERVAL '1 day' + INTERVAL '6 hours'),
			('two-b', 'busy', '2', CURRENT_DATE - INTERVAL '1 day' + INTERVAL '6 hours 10 minutes'),
			('two-c', 'busy', '2', CURRENT_DATE - INTERVAL '1 day' + INTERVAL '6 hours 30 minutes'),
			('one-a', 'busy', '1', CURRENT_DATE - INTERVAL '1 day' + INTERVAL '7 hours'),
			('one-b', 'busy', '1', CURRENT_DATE - INTERVAL '1 day' + INTERVAL '7 hours 30 minutes'),
			('future', 'busy', '1', CURRENT_DATE + INTERVAL '1 day'),
			('ordinary-a', 'ordinary', '1', CURRENT_DATE - INTERVAL '1 day' + INTERVAL '6 hours'),
			('ordinary-b', 'ordinary', '1', CURRENT_DATE - INTERVAL '1 day' + INTERVAL '6 hours 20 minutes'),
			('before-boundary', 'boundary', '1', CURRENT_DATE - INTERVAL '7 days 1 microsecond'),
			('at-boundary', 'boundary', '1', CURRENT_DATE - INTERVAL '7 days'),
			('single', 'single', '1', CURRENT_DATE - INTERVAL '1 day'),
			('old-only', 'old-only', '1', CURRENT_DATE - INTERVAL '10 days'),
			('no-latitude', 'no-latitude', '1', CURRENT_DATE - INTERVAL '1 day'),
			('no-longitude', 'no-longitude', '1', CURRENT_DATE - INTERVAL '1 day');
	`)
	rows, err := NewRepo(db).StopAnalytics(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d stops, want four recent stops with coordinates: %+v", len(rows), rows)
	}
	byStop := make(map[string]StopAnalyticsRow)
	for i, row := range rows {
		byStop[row.StopID] = row
		if i > 0 && rows[i-1].TotalVisits < row.TotalVisits {
			t.Errorf("visits are not descending: %+v", rows)
		}
		// Tied counts have no defined secondary order. The original all-history
		// timestamp query also includes future records and uses session TEXT formatting.
		var lastServiced string
		if err := db.QueryRow(t.Context(), `SELECT MAX(observed_at)::TEXT FROM transit.stop_visit WHERE stop_id = $1`, row.StopID).Scan(&lastServiced); err != nil {
			t.Fatal(err)
		}
		if row.LastServiced == nil || *row.LastServiced != lastServiced {
			t.Errorf("%s last serviced = %v, want %s", row.StopID, row.LastServiced, lastServiced)
		}
	}
	busy := byStop["busy"]
	if busy.TotalVisits != 6 || busy.RoutesServing != 2 || !reflect.DeepEqual(busy.RouteIDs, []string{"1", "2"}) {
		t.Errorf("recent visits and distinct routes = %+v", busy)
	}
	if busy.AvgHeadwayMin == nil || *busy.AvgHeadwayMin != 20 {
		t.Errorf("headway = %v, want average of 10, 20, and 30 minutes", busy.AvgHeadwayMin)
	}
	for _, id := range []string{"ordinary", "boundary", "single"} {
		row, ok := byStop[id]
		if !ok || row.AvgHeadwayMin != nil {
			t.Errorf("%s should be present with a null headway: %+v", id, row)
		}
	}
	if byStop["boundary"].TotalVisits != 1 || byStop["ordinary"].TotalVisits != 2 || byStop["single"].StopName != "single" {
		t.Errorf("boundary count, ordinary count, or null-name fallback changed: %+v", byStop)
	}
}
