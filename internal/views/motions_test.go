package views

import (
	"testing"

	"thundercitizen/internal/council"
)

func TestBuildVoteRoster_Nil(t *testing.T) {
	if got := BuildVoteRoster(nil, "CARRIED", nil); got != nil {
		t.Fatalf("expected nil for nil VoteRecord, got %+v", got)
	}
}

func TestBuildVoteRoster_EmptyTallies(t *testing.T) {
	rec := &council.VoteRecord{Absent: []string{"Someone"}}
	if got := BuildVoteRoster(rec, "CARRIED", nil); got != nil {
		t.Fatalf("expected nil when no For/Against votes, got %+v", got)
	}
}

func TestBuildVoteRoster_UnanimousFor(t *testing.T) {
	rec := &council.VoteRecord{For: []string{"A", "B", "C"}}
	v := BuildVoteRoster(rec, "CARRIED", nil)
	if v == nil {
		t.Fatal("expected non-nil")
	}
	if !v.Unanimous || !v.UnanimousFor {
		t.Errorf("expected Unanimous && UnanimousFor, got %+v", v)
	}
	if v.Headline != "CARRIED UNANIMOUSLY" {
		t.Errorf("headline = %q, want CARRIED UNANIMOUSLY", v.Headline)
	}
	if v.Tally != "3–0" {
		t.Errorf("tally = %q, want 3–0", v.Tally)
	}
	if v.HeadlineCls != "carried" {
		t.Errorf("HeadlineCls = %q, want carried", v.HeadlineCls)
	}
}

func TestBuildVoteRoster_UnanimousAgainst(t *testing.T) {
	rec := &council.VoteRecord{Against: []string{"A", "B"}}
	v := BuildVoteRoster(rec, "LOST", nil)
	if !v.Unanimous || v.UnanimousFor {
		t.Errorf("expected Unanimous and !UnanimousFor, got %+v", v)
	}
	if v.Headline != "LOST UNANIMOUSLY" {
		t.Errorf("headline = %q", v.Headline)
	}
	if v.Tally != "0–2" {
		t.Errorf("tally = %q", v.Tally)
	}
}

func TestBuildVoteRoster_Split(t *testing.T) {
	rec := &council.VoteRecord{
		For:     []string{"A", "B", "C", "D", "E", "F", "G"},
		Against: []string{"H", "I", "J", "K", "L", "M"},
	}
	v := BuildVoteRoster(rec, "CARRIED", nil)
	if v.Unanimous {
		t.Error("expected !Unanimous")
	}
	if v.Tally != "7–6" {
		t.Errorf("tally = %q", v.Tally)
	}
	if v.Headline != "CARRIED" {
		t.Errorf("headline = %q", v.Headline)
	}
}

func TestBuildVoteRoster_Tie(t *testing.T) {
	rec := &council.VoteRecord{
		For:     []string{"A"},
		Against: []string{"B"},
	}
	v := BuildVoteRoster(rec, "TIE", nil)
	if v.Headline != "TIED" {
		t.Errorf("headline = %q, want TIED", v.Headline)
	}
	if v.HeadlineCls != "tie" {
		t.Errorf("HeadlineCls = %q, want tie", v.HeadlineCls)
	}
}

func TestBuildVoteRoster_PhotoLookup(t *testing.T) {
	rec := &council.VoteRecord{For: []string{"Known", "Missing"}}
	photos := map[string]string{"Known": "/static/councillors/known.jpg"}
	v := BuildVoteRoster(rec, "CARRIED", photos)
	if v.For[0].Photo != "/static/councillors/known.jpg" {
		t.Errorf("Known.Photo = %q", v.For[0].Photo)
	}
	if v.For[1].Photo != "" {
		t.Errorf("Missing.Photo = %q, want empty", v.For[1].Photo)
	}
	if v.For[1].Initials == "" {
		t.Error("Missing.Initials should fall back to non-empty")
	}
}
