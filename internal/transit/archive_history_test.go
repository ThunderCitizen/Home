package transit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArchivedHistoryLinksToRetainedSummaries(t *testing.T) {
	db := routeQueryTestDB(t)
	routeQueryExec(t, db, `INSERT INTO transit.archive_month VALUES ('2026-08-01','published')`)
	// No metadata or raw readers are configured: archive handling must happen first.
	h := &Handler{svc: &Service{db: db}}
	for _, path := range []string{
		"/route/R?date=2026-08-03&partial=schedule-body",
		"/route/R?date=2026-08-03&partial=schedule",
		"/audit/deltas/R?date=2026-08-03",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.PageRoutes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusGone {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `href="/transit/routes?date=2026-08-03"`) {
				t.Fatal(w.Body.String())
			}
		})
	}
}
