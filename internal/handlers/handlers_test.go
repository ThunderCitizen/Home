package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"thundercitizen/internal/cache"
	"thundercitizen/internal/council"
	"thundercitizen/internal/httperr"
)

type stubCouncilStore struct {
	listMeetingSummaries   func(ctx context.Context, f council.MeetingFilter) ([]council.MeetingSummary, int, error)
	councillorVoteStatsAll func(ctx context.Context, term string) (map[string]council.CouncillorVoteStats, error)
	voteMatrix             func(ctx context.Context, term string) ([]council.VoteMatrixMotion, []council.VoteMatrixRecord, error)
	motionStats            func(ctx context.Context, term string) (int, int, int, error)
	searchMotions          func(ctx context.Context, f council.MotionFilter) ([]council.MotionRow, int, error)
	getMeetingByID         func(ctx context.Context, id string) (*council.MeetingDetail, error)
	getMeetingBySlug       func(ctx context.Context, slug string) (*council.MeetingDetail, error)
	loadVoteRecords        func(ctx context.Context, motionID int64) (*council.VoteRecord, error)
}

func (s stubCouncilStore) ListMeetingSummaries(ctx context.Context, f council.MeetingFilter) ([]council.MeetingSummary, int, error) {
	return s.listMeetingSummaries(ctx, f)
}
func (s stubCouncilStore) MeetingKeyItems(ctx context.Context, meetingID string, limit int) ([]string, error) {
	return nil, nil
}
func (s stubCouncilStore) CouncillorVoteStatsAll(ctx context.Context, term string) (map[string]council.CouncillorVoteStats, error) {
	return s.councillorVoteStatsAll(ctx, term)
}
func (s stubCouncilStore) VoteMatrix(ctx context.Context, term string) ([]council.VoteMatrixMotion, []council.VoteMatrixRecord, error) {
	return s.voteMatrix(ctx, term)
}
func (s stubCouncilStore) MotionStats(ctx context.Context, term string) (int, int, int, error) {
	return s.motionStats(ctx, term)
}
func (s stubCouncilStore) SearchMotions(ctx context.Context, f council.MotionFilter) ([]council.MotionRow, int, error) {
	return s.searchMotions(ctx, f)
}
func (s stubCouncilStore) GetMeetingByID(ctx context.Context, id string) (*council.MeetingDetail, error) {
	return s.getMeetingByID(ctx, id)
}
func (s stubCouncilStore) GetMeetingBySlug(ctx context.Context, slug string) (*council.MeetingDetail, error) {
	if s.getMeetingBySlug != nil {
		return s.getMeetingBySlug(ctx, slug)
	}
	// Default: tests target the UUID/ID path — return ErrNoRows so the
	// CouncilMeeting handler falls through to GetMeetingByID.
	return nil, pgx.ErrNoRows
}
func (s stubCouncilStore) LoadVoteRecords(ctx context.Context, motionID int64) (*council.VoteRecord, error) {
	return s.loadVoteRecords(ctx, motionID)
}
func (s stubCouncilStore) MeetingIDsByDates(ctx context.Context, dates []string) (map[string]string, error) {
	return nil, nil
}
func (s stubCouncilStore) LastScrapedAt(ctx context.Context) (time.Time, error) {
	return time.Time{}, nil
}

func assertUnavailable(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rr.Code)
	}
	var resp httperr.Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 in body, got %d", resp.Code)
	}
}

func TestElection2026RendersStaticGuide(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/election/2026", nil)
	rr := httptest.NewRecorder()

	(&Handlers{}).Election2026(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != cache.Page {
		t.Errorf("Cache-Control = %q, want %q", got, cache.Page)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", got)
	}
	if !strings.Contains(rr.Body.String(), "2026 Municipal Election") {
		t.Error("response does not contain the election heading")
	}
}

func TestElection2026CandidatesCSV(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/election/2026/candidates.csv", nil)
	rr := httptest.NewRecorder()

	(&Handlers{}).Election2026CandidatesCSV(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got != `attachment; filename="thunder-bay-2026-candidates.csv"` {
		t.Errorf("Content-Disposition = %q", got)
	}

	rows, err := csv.NewReader(strings.NewReader(rr.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	if got, want := len(rows), 73; got != want { // header plus every candidate card
		t.Errorf("CSV rows = %d, want %d", got, want)
	}
	if got := rows[0][0]; got != "contest" {
		t.Errorf("first header = %q, want contest", got)
	}
	for _, header := range rows[0] {
		if header == "city_profile_url" || header == "candidate_page_note" || header == "vote_instruction" {
			t.Errorf("CSV must not include %s", header)
		}
	}
	if got := rows[1][3]; got != "Maureen (Moe) Comuzzi" {
		t.Errorf("first candidate = %q", got)
	}
}

func TestElectionAliasRedirectsTemporarily(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/election", nil)
	rr := httptest.NewRecorder()

	(&Handlers{}).Election(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "/election/2026" {
		t.Errorf("Location = %q, want /election/2026", got)
	}
}

func TestCouncillorsReturnsUnavailableWhenVoteMatrixFails(t *testing.T) {
	orig := newCouncilStore
	t.Cleanup(func() { newCouncilStore = orig })

	newCouncilStore = func(_ *pgxpool.Pool) councilStore {
		return stubCouncilStore{
			councillorVoteStatsAll: func(context.Context, string) (map[string]council.CouncillorVoteStats, error) {
				return map[string]council.CouncillorVoteStats{}, nil
			},
			voteMatrix: func(context.Context, string) ([]council.VoteMatrixMotion, []council.VoteMatrixRecord, error) {
				return nil, nil, errors.New("vote matrix failed")
			},
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/councillors", nil)
	rr := httptest.NewRecorder()

	(&Handlers{}).Councillors(rr, req)

	assertUnavailable(t, rr)
}

func TestCouncilReturnsUnavailableWhenMotionStatsFails(t *testing.T) {
	orig := newCouncilStore
	t.Cleanup(func() { newCouncilStore = orig })

	newCouncilStore = func(_ *pgxpool.Pool) councilStore {
		return stubCouncilStore{
			listMeetingSummaries: func(context.Context, council.MeetingFilter) ([]council.MeetingSummary, int, error) {
				return nil, 0, nil
			},
			motionStats: func(context.Context, string) (int, int, int, error) {
				return 0, 0, 0, errors.New("motion stats failed")
			},
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/minutes", nil)
	rr := httptest.NewRecorder()

	(&Handlers{}).Council(rr, req)

	assertUnavailable(t, rr)
}

func TestCouncilMeetingReturnsUnavailableWhenVoteRecordsFail(t *testing.T) {
	orig := newCouncilStore
	t.Cleanup(func() { newCouncilStore = orig })

	newCouncilStore = func(_ *pgxpool.Pool) councilStore {
		return stubCouncilStore{
			// Resolve the slug directly so the handler skips the
			// slug-to-ID fallback redirect and proceeds to load
			// vote records — which fails, triggering the 503 we
			// want to assert on.
			getMeetingBySlug: func(context.Context, string) (*council.MeetingDetail, error) {
				return &council.MeetingDetail{
					ID: "m1",
					Motions: []council.MotionRow{
						{ID: 42, YeaCount: 1},
					},
				}, nil
			},
			loadVoteRecords: func(context.Context, int64) (*council.VoteRecord, error) {
				return nil, errors.New("vote records failed")
			},
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/minutes/m1", nil)
	req.SetPathValue("id", "m1")
	rr := httptest.NewRecorder()

	(&Handlers{}).CouncilMeeting(rr, req)

	assertUnavailable(t, rr)
}
