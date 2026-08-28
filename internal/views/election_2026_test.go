package views

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func candidateNames(candidates []ElectionCandidateView) []string {
	names := make([]string, len(candidates))
	for i, candidate := range candidates {
		names[i] = candidate.Name
	}
	return names
}

func TestElection2026MunicipalRoster(t *testing.T) {
	vm := NewElection2026ViewModel()

	if got := vm.MunicipalCandidateCount(); got != 46 {
		t.Fatalf("municipal candidates = %d, want 46", got)
	}
	if got := vm.TrusteeCandidateCount(); got != 26 {
		t.Fatalf("trustee candidates = %d, want 26", got)
	}

	tests := []struct {
		name       string
		contest    ElectionContestView
		seats      int
		candidates []string
	}{
		{"Mayor", vm.Mayor, 1, []string{"Maureen (Moe) Comuzzi", "Peter Diedrich", "Trevor Giertuga", "Shane Judge", "Volker Kromm", "Peter Panetta", "Aldo Ruberto", "Wolfgang Schoor", "Doug Vincent", "Donald Baxter"}},
		{"At-Large", vm.AtLarge, 5, []string{"Rajni Agarwal", "Mark Bentz", "Gene Capar", "Gary Christian", "Julie Colquhoun", "Patrick George Cully", "Heather K. Dahlstrom", "Stephanie Danylko", "Kasey Taylor Etreni", "Tyler Goode", "Dino Menei", "Jamie Nichols", "Robert Trevisan", "Peng You"}},
	}

	wardNames := []string{"Current River", "Red River", "McIntyre", "McKellar", "Northwood", "Westfort", "Neebing"}
	wardCounts := []int{2, 5, 2, 5, 3, 3, 2}
	if len(vm.Wards) != len(wardNames) {
		t.Fatalf("ward contests = %d, want %d", len(vm.Wards), len(wardNames))
	}
	for i, ward := range vm.Wards {
		if ward.Name != wardNames[i] {
			t.Errorf("ward %d = %q, want %q", i, ward.Name, wardNames[i])
		}
		if ward.Seats != 1 {
			t.Errorf("%s seats = %d, want 1", ward.Name, ward.Seats)
		}
		if len(ward.Candidates) != wardCounts[i] {
			t.Errorf("%s candidates = %d, want %d", ward.Name, len(ward.Candidates), wardCounts[i])
		}
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.contest.Seats != tc.seats {
				t.Errorf("seats = %d, want %d", tc.contest.Seats, tc.seats)
			}
			if got := candidateNames(tc.contest.Candidates); !reflect.DeepEqual(got, tc.candidates) {
				t.Errorf("candidates = %#v\nwant %#v", got, tc.candidates)
			}
		})
	}

	trusteeCounts := []int{12, 8, 1, 5}
	if len(vm.Trustees) != len(trusteeCounts) {
		t.Fatalf("trustee contests = %d, want %d", len(vm.Trustees), len(trusteeCounts))
	}
	for i, contest := range vm.Trustees {
		if len(contest.Candidates) != trusteeCounts[i] {
			t.Errorf("%s candidates = %d, want %d", contest.Name, len(contest.Candidates), trusteeCounts[i])
		}
	}
	for _, contest := range []ElectionContestView{vm.Trustees[2], vm.Trustees[3]} {
		if !contest.Acclaimed {
			t.Errorf("%s must be acclaimed", contest.Name)
		}
	}
}

func TestElection2026CandidateOrderAndUniqueness(t *testing.T) {
	vm := NewElection2026ViewModel()
	contests := []ElectionContestView{vm.Mayor, vm.AtLarge}
	contests = append(contests, vm.Wards...)
	contests = append(contests, vm.Trustees...)

	seen := make(map[string]string)
	for _, contest := range contests {
		if contest.Name == "Mayor" {
			if last := contest.Candidates[len(contest.Candidates)-1]; last.Name != "Donald Baxter" {
				t.Errorf("Mayor list ends with %q, want Donald Baxter", last.Name)
			}
		} else if !sort.SliceIsSorted(contest.Candidates, func(i, j int) bool {
			return contest.Candidates[i].SortName < contest.Candidates[j].SortName
		}) {
			t.Errorf("%s candidates are not in surname order", contest.Name)
		}
		for _, candidate := range contest.Candidates {
			if candidate.Name == "" || candidate.SortName == "" || candidate.Summary == "" {
				t.Errorf("%s has an incomplete card model", candidate.Name)
			}
			if previous, ok := seen[candidate.Name]; ok {
				t.Errorf("candidate %q appears in both %s and %s", candidate.Name, previous, contest.Name)
			}
			seen[candidate.Name] = contest.Name
		}
	}
	if got := len(seen); got != 72 {
		t.Errorf("unique candidates = %d, want 72", got)
	}
}

func TestElection2026MunicipalIncumbencyIsPrecise(t *testing.T) {
	vm := NewElection2026ViewModel()
	municipal := append([]ElectionContestView{vm.Mayor, vm.AtLarge}, vm.Wards...)

	want := map[string]string{
		"Trevor Giertuga":     "At-Large Councillor",
		"Rajni Agarwal":       "At-Large Councillor",
		"Mark Bentz":          "At-Large Councillor",
		"Kasey Taylor Etreni": "At-Large Councillor",
		"Andrew Foulds":       "Ward Councillor",
		"Albert Aiello":       "Ward Councillor",
		"Brian Hamilton":      "Ward Councillor",
		"Greg Johnsen":        "Ward Councillor",
	}

	got := make(map[string]string)
	for _, contest := range municipal {
		for _, candidate := range contest.Candidates {
			if strings.Contains(strings.ToLower(candidate.OfficeStatus), "councillor") {
				got[candidate.Name] = candidate.OfficeStatus
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("council office statuses = %#v\nwant %#v", got, want)
	}
	if strings.Contains(strings.ToLower(got["Trevor Giertuga"]), "incumbent") {
		t.Error("Trevor Giertuga must not be described as the incumbent mayor")
	}
}

func TestElection2026CandidatePageProvenance(t *testing.T) {
	vm := NewElection2026ViewModel()
	allowed := map[ElectionPageKind]bool{
		ElectionPageCampaign:     true,
		ElectionPageCandidate:    true,
		ElectionPageProfessional: true,
	}
	contests := []ElectionContestView{vm.Mayor, vm.AtLarge}
	contests = append(contests, vm.Wards...)
	contests = append(contests, vm.Trustees...)

	for _, contest := range contests {
		for _, candidate := range contest.Candidates {
			for _, social := range candidate.Socials {
				if social.Platform == "" || !strings.HasPrefix(social.URL, "https://") {
					t.Errorf("%s has an invalid social profile: %#v", candidate.Name, social)
				}
				if social.Platform == "Facebook" && social.DisplayLabel() != "Facebook profile" && social.DisplayLabel() != "Facebook campaign announcement" {
					t.Errorf("%s has an unclear Facebook link label %q", candidate.Name, social.DisplayLabel())
				}
			}
			if candidate.Page == nil {
				continue
			}
			if !allowed[candidate.Page.Kind] {
				t.Errorf("%s has unsupported page kind %q", candidate.Name, candidate.Page.Kind)
			}
			if !strings.HasPrefix(candidate.Page.URL, "https://") {
				t.Errorf("%s page is not HTTPS: %s", candidate.Name, candidate.Page.URL)
			}
		}
	}

	for _, candidate := range vm.AtLarge.Candidates {
		if candidate.Name == "Tyler Goode" && candidate.Page != nil {
			t.Error("Tyler Goode must not link to the unrelated aerospace website")
		}
	}
}
