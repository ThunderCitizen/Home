package views

import (
	"testing"

	"thundercitizen/internal/data"
)

func TestBuildCouncilActivitySortsByCalendarDate(t *testing.T) {
	recent := []RecentMeetingView{
		{DateISO: "2026-06-23", Date: "June 23, 2026", Slug: "city-council-2026-06-23"},
		{DateISO: "2026-05-19", Date: "May 19, 2026", Slug: "city-council-2026-05-19"},
	}
	pending := []data.PendingMeeting{
		{DateISO: "2026-06-02", Date: "Tuesday, June 2, 2026"},
	}

	got := buildCouncilActivity(recent, pending)
	wantDates := []string{"June 23, 2026", "Tuesday, June 2, 2026", "May 19, 2026"}
	if len(got) != len(wantDates) {
		t.Fatalf("activity len=%d, want %d", len(got), len(wantDates))
	}
	for i, want := range wantDates {
		if got[i].Date != want {
			t.Fatalf("activity[%d].Date=%q, want %q", i, got[i].Date, want)
		}
	}
	if !got[1].Pending {
		t.Fatal("June 2 row should remain marked pending")
	}
}
