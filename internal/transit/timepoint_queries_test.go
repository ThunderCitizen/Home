package transit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRepoTimepointStops(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `
		CREATE TABLE transit.route (route_id text PRIMARY KEY, color text NOT NULL DEFAULT '');
		CREATE TABLE transit.route_pattern (pattern_id text PRIMARY KEY, route_id text NOT NULL);
		CREATE TABLE transit.route_pattern_stop (
			pattern_id text, sequence int, stop_id text NOT NULL, is_timepoint boolean NOT NULL,
			PRIMARY KEY (pattern_id, sequence)
		);
	`)
	repo := NewRepo(db)
	handler := &Handler{svc: &Service{reporter: &Reporter{repo: repo}}}
	checkAPI := func(want []MapTimepointStop) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.timepoints(response, httptest.NewRequest(http.MethodGet, "/api/transit/timepoints", nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("timepoints response: status=%d type=%q", response.Code, response.Header().Get("Content-Type"))
		}
		expected, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if body := strings.TrimSpace(response.Body.String()); body != string(expected) {
			t.Fatalf("timepoints JSON = %s; want %s", body, expected)
		}
	}
	if got, err := repo.TimepointStops(t.Context()); err != nil || got != nil {
		t.Fatalf("empty timepoints = %#v, %v; want nil, nil", got, err)
	}
	checkAPI(nil) // Preserve the existing empty endpoint response: null.

	// The reduced fixture omits foreign keys to also exercise the existing
	// missing-metadata fallback and exclusion of absent stop IDs.
	routeQueryExec(t, db, `
		INSERT INTO transit.route VALUES
			('1', '#shared'), ('2', '#shared'), ('3', ''), ('10', '#other');
		INSERT INTO transit.stop VALUES
			('', ''), ('shared', 'Shared stop'), ('solo', 'Solo stop'), ('regular', 'Regular stop');
		INSERT INTO transit.route_pattern VALUES
			('p1a', '1'), ('p1b', '1'), ('p2', '2'), ('p3', '3'), ('p10', '10'), ('orphan', 'missing');
		INSERT INTO transit.route_pattern_stop VALUES
			('p1a', 1, 'shared', true),
			('p1a', 2, 'shared', true),
			('p1b', 1, 'shared', true),
			('p2', 1, 'shared', true),
			('p3', 1, 'shared', true),
			('p10', 1, 'shared', true),
			('orphan', 1, 'shared', true),
			('p1a', 3, 'solo', true),
			('p1a', 4, 'missing-stop', true),
			('p2', 2, 'regular', false),
			('p2', 3, '', true);
	`)
	want := []MapTimepointStop{
		{StopID: "", Routes: []string{"2"}, Colors: []string{"#shared"}},
		{StopID: "shared", Routes: []string{"1", "10", "2", "3", "missing"}, Colors: []string{"#shared", "#other", "#shared", "", ""}},
		{StopID: "solo", Routes: []string{"1"}, Colors: []string{"#shared"}},
	}
	got, err := repo.TimepointStops(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("timepoints = %#v; want %#v", got, want)
	}
	checkAPI(want)
}
