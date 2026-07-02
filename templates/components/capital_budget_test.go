package components

import "testing"

func TestCapitalProjectPublicSection(t *testing.T) {
	tests := []struct {
		name    string
		project CapitalProjectItem
		want    string
	}{
		{
			name: "ems hq from municipal source",
			project: CapitalProjectItem{
				ID:       "com-ems-0008-em-superior-north-ems-hq-expansion",
				Name:     "Superior North EMS HQ Expansion",
				Service:  "Municipal Offices and Facilities",
				Category: "Municipal Offices and Facilities",
			},
			want: "EMS",
		},
		{
			name: "police equipment from bad parks source",
			project: CapitalProjectItem{
				ID:       "osb-pol-0038-ps-in-car-body-worn-cameras",
				Name:     "In Car & Body Worn Cameras",
				Service:  "Parks and Open Spaces",
				Category: "Equipment",
			},
			want: "Police",
		},
		{
			name: "transit from bad long term care source",
			project: CapitalProjectItem{
				ID:       "com-trn-0002-ct-001-shelter-replacement-accessible-stop",
				Name:     "Shelter Replacement",
				Service:  "Long-Term Care - Pioneer Ridge",
				Category: "Facilities",
			},
			want: "Transit",
		},
		{
			name: "watermain",
			project: CapitalProjectItem{
				ID:       "ior-waw-0004-wr-014-phase-iv-sheep-ranch",
				Name:     "Watermain Replacement Phase IV Sheep Ranch",
				Service:  "Waterworks",
				Category: "Network (Renewal and Replacement)",
			},
			want: "Water",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capitalProjectPublicSection(tt.project); got != tt.want {
				t.Fatalf("capitalProjectPublicSection() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCapitalProjectSectionRollup(t *testing.T) {
	tests := []struct {
		name    string
		project CapitalProjectItem
		wantTag string
		want    string
	}{
		{
			name: "ems hq remains ems but filters as emergency",
			project: CapitalProjectItem{
				ID:       "com-ems-0008-em-superior-north-ems-hq-expansion",
				Name:     "Superior North EMS HQ Expansion",
				Service:  "Municipal Offices and Facilities",
				Category: "Municipal Offices and Facilities",
			},
			wantTag: "EMS",
			want:    "Emergency",
		},
		{
			name: "water filters as water and wastewater",
			project: CapitalProjectItem{
				ID:       "ior-waw-0004-wr-014-phase-iv-sheep-ranch",
				Name:     "Watermain Replacement Phase IV Sheep Ranch",
				Service:  "Waterworks",
				Category: "Network (Renewal and Replacement)",
			},
			wantTag: "Water",
			want:    "Water & Wastewater",
		},
		{
			name: "roads filter separately",
			project: CapitalProjectItem{
				ID:       "iot-rds-0004-rn-028-annual-resurfacing-program",
				Name:     "Annual Resurfacing Program",
				Service:  "Roads",
				Category: "Network (Renewal and Replacement)",
			},
			wantTag: "Roads",
			want:    "Roads",
		},
		{
			name: "solid waste filters separately",
			project: CapitalProjectItem{
				ID:       "iot-sow-0003-sw-009-landfill-gas-system",
				Name:     "Landfill Gas System",
				Service:  "Solid Waste and Diversion",
				Category: "Network (Renewal and Replacement)",
			},
			wantTag: "Waste",
			want:    "Waste",
		},
		{
			name: "equipment filters as vehicles and gear",
			project: CapitalProjectItem{
				ID:       "cor-oct-0004-sm-printing-graphics",
				Name:     "Printing & Graphics",
				Service:  "Initiatives and Support",
				Category: "Equipment",
			},
			wantTag: "Equipment",
			want:    "Vehicles & Gear",
		},
		{
			name: "transit filters separately",
			project: CapitalProjectItem{
				ID:       "com-trn-0002-ct-001-shelter-replacement-accessible-stop",
				Name:     "Shelter Replacement",
				Service:  "Transit Services",
				Category: "Facilities",
			},
			wantTag: "Transit",
			want:    "Transit",
		},
		{
			name: "parking filters separately",
			project: CapitalProjectItem{
				ID:       "com-pkg-0001-pa-parking-meter-replacement",
				Name:     "Parking Meter Replacement",
				Service:  "Parking Authority",
				Category: "Equipment",
			},
			wantTag: "Parking",
			want:    "Parking",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capitalProjectSectionTag(tt.project); got != tt.wantTag {
				t.Fatalf("capitalProjectSectionTag() = %q, want %q", got, tt.wantTag)
			}
			if got := capitalProjectSectionRollup(tt.project); got != tt.want {
				t.Fatalf("capitalProjectSectionRollup() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCapitalProjectDistinctPlaceSuppressesNameRepeats(t *testing.T) {
	if got := capitalProjectDistinctPlace("Superior North EMS HQ Expansion", "Superior North EMS HQ"); got != "" {
		t.Fatalf("capitalProjectDistinctPlace() = %q, want empty duplicate", got)
	}
	if got := capitalProjectDistinctPlace("Mission Island Bridge", "Mission Island"); got != "" {
		t.Fatalf("capitalProjectDistinctPlace() = %q, want empty duplicate", got)
	}
	if got := capitalProjectDistinctPlace("Structural Culvert Replacement", "Pacific Avenue"); got != "Pacific Avenue" {
		t.Fatalf("capitalProjectDistinctPlace() = %q, want Pacific Avenue", got)
	}
}

func TestCapitalProjectSourceLineSummary(t *testing.T) {
	lines := []CapitalSourceLineItem{
		{Category: "Equipment", AmountLabel: "$226K"},
		{Category: "Fleet", AmountLabel: "$65K"},
	}
	want := "This code appears on 2 official budget rows: Equipment $226K · Fleet $65K."
	if got := capitalProjectSourceLineSummary(2, lines); got != want {
		t.Fatalf("capitalProjectSourceLineSummary() = %q, want %q", got, want)
	}
}

func TestCapitalProjectSourceLineSummarySingleCategory(t *testing.T) {
	lines := []CapitalSourceLineItem{
		{Category: "Fleet", AmountLabel: "$5.6M"},
	}
	want := "This code appears on 3 official budget rows in Fleet $5.6M."
	if got := capitalProjectSourceLineSummary(3, lines); got != want {
		t.Fatalf("capitalProjectSourceLineSummary() = %q, want %q", got, want)
	}
}

func TestCapitalCompactDollarsLabelUsesThousandsBelowMillion(t *testing.T) {
	tests := map[float64]string{
		65_000:    "$65K",
		226_000:   "$226K",
		999_999:   "$1000K",
		1_000_000: "$1.0M",
	}
	for dollars, want := range tests {
		if got := capitalCompactDollarsLabel(dollars); got != want {
			t.Fatalf("capitalCompactDollarsLabel(%v) = %q, want %q", dollars, got, want)
		}
	}
}
