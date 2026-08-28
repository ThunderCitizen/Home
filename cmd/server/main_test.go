package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"thundercitizen/internal/cache"
	"thundercitizen/internal/handlers"
)

func TestElection2026Route(t *testing.T) {
	r := chi.NewRouter()
	registerPageRoutes(r, &handlers.Handlers{})

	req := httptest.NewRequest(http.MethodGet, "/election/2026", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != cache.Page {
		t.Errorf("Cache-Control = %q, want %q", got, cache.Page)
	}
}

func TestElection2026CandidatesCSVRoute(t *testing.T) {
	r := chi.NewRouter()
	registerPageRoutes(r, &handlers.Handlers{})

	req := httptest.NewRequest(http.MethodGet, "/election/2026/candidates.csv", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestElectionRouteAlias(t *testing.T) {
	r := chi.NewRouter()
	registerPageRoutes(r, &handlers.Handlers{})

	req := httptest.NewRequest(http.MethodGet, "/election", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "/election/2026" {
		t.Errorf("Location = %q, want /election/2026", got)
	}
}
