package views

import (
	"strings"
	"testing"

	"thundercitizen/internal/budget"
)

func TestCapitalProjectAmountLabelUsesThousandsBelowMillion(t *testing.T) {
	tests := map[float64]string{
		65_000:    "$65K",
		226_000:   "$226K",
		999_999:   "$1000K",
		1_000_000: "$1.0M",
	}
	for dollars, want := range tests {
		if got := capitalProjectAmountLabel(dollars); got != want {
			t.Fatalf("capitalProjectAmountLabel(%v) = %q, want %q", dollars, got, want)
		}
	}
}

func TestCapitalBudgetSankeyDescriptionIncludesProjectContext(t *testing.T) {
	got := capitalBudgetSankeyDescription(347)
	for _, want := range []string{"347 approved project lines", "roads", "water and sewer"} {
		if !strings.Contains(got, want) {
			t.Fatalf("capitalBudgetSankeyDescription() = %q, want to contain %q", got, want)
		}
	}
}

func TestOperatingTopLineSummaryUsesOfficialLevySplit(t *testing.T) {
	got := operatingTopLineSummary(2026, &budget.OperatingSummary{
		TotalExpenditure: 478_399_999.98,
		PropertyTax:      274_592_611.33,
		Grants:           126_100_000,
		OtherRevenue:     77_707_388.65,
	})

	if got.Total != 478_730_700 {
		t.Fatalf("Total = %.2f, want 478730700", got.Total)
	}
	if got.Levy != 227_388_800 {
		t.Fatalf("Levy = %.2f, want 227388800", got.Levy)
	}
	if got.Grants != 81_578_900 {
		t.Fatalf("Grants = %.2f, want 81578900", got.Grants)
	}
	if got.OtherSources != 169_763_000 {
		t.Fatalf("OtherSources = %.2f, want 169763000", got.OtherSources)
	}
}

func TestCapitalProjectSourceLineGroupsUseCategoryWhenItSplits(t *testing.T) {
	projects := []budget.CapitalProject{
		{
			ID:       "com-hom-0022-pj-007-food-delivery-vehicle",
			Name:     "Food Delivery Vehicle",
			Service:  "Housing",
			Category: "Fleet",
			Years:    []budget.CapitalProjectYear{{Amount: 65_000}},
		},
		{
			ID:       "com-hom-0022-pj-007-replacements-residential-furnishings",
			Name:     "Residential Furnishings",
			Service:  "Housing",
			Category: "Equipment",
			Years:    []budget.CapitalProjectYear{{Amount: 226_000}},
		},
	}

	lines := capitalProjectSourceLineGroups(projects)["COM-HOM-0022-PJ-007"].Lines
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0].Category != "Equipment" || lines[0].AmountLabel != "$226K" {
		t.Fatalf("first line = %+v, want Equipment $226K", lines[0])
	}
	if lines[1].Category != "Fleet" || lines[1].AmountLabel != "$65K" {
		t.Fatalf("second line = %+v, want Fleet $65K", lines[1])
	}
}

func TestCapitalProjectSourceLineGroupsUseServiceWhenCategoryDoesNotSplit(t *testing.T) {
	projects := []budget.CapitalProject{
		{
			ID:       "com-flt-0007-fl-001-roads-fleet-renewal",
			Name:     "Roads Fleet Renewal",
			Service:  "Roads",
			Category: "Fleet",
			Years:    []budget.CapitalProjectYear{{Amount: 2_437_100}, {Amount: 2_656_200}},
		},
		{
			ID:       "com-flt-0007-fl-001-diversion-fleet-renewal",
			Name:     "Solid Waste and Diversion Fleet Renewal",
			Service:  "Solid Waste and Diversion",
			Category: "Fleet",
			Years:    []budget.CapitalProjectYear{{Amount: 520_000}},
		},
		{
			ID:       "com-flt-0007-fl-001-parks-fleet-renewal",
			Name:     "Parks Fleet Renewal",
			Service:  "Arenas, Stadia, and Aquatics",
			Category: "Fleet",
			Years:    []budget.CapitalProjectYear{{Amount: 350_000}},
		},
	}

	lines := capitalProjectSourceLineGroups(projects)["COM-FLT-0007-FL-001"].Lines
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0].Category != "Roads" || lines[0].AmountLabel != "$5.1M" {
		t.Fatalf("first line = %+v, want Roads $5.1M", lines[0])
	}
	if lines[1].Category != "Solid Waste and Diversion" || lines[1].AmountLabel != "$520K" {
		t.Fatalf("second line = %+v, want Solid Waste and Diversion $520K", lines[1])
	}
	if lines[2].Category != "Arenas, Stadia, and Aquatics" || lines[2].AmountLabel != "$350K" {
		t.Fatalf("third line = %+v, want Arenas, Stadia, and Aquatics $350K", lines[2])
	}
}

func TestSortCapitalProjectsByTotalDesc(t *testing.T) {
	projects := []CapitalProjectView{
		{Name: "Sidewalks", TotalAmount: 500_000},
		{Name: "Watermain", TotalAmount: 2_000_000},
		{Name: "Arena", TotalAmount: 2_000_000},
	}

	sortCapitalProjectsByTotalDesc(projects)

	got := []string{projects[0].Name, projects[1].Name, projects[2].Name}
	want := []string{"Arena", "Watermain", "Sidewalks"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted names = %v, want %v", got, want)
		}
	}
}
