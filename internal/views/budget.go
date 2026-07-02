package views

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"thundercitizen/internal/budget"
)

// DefaultBudgetYear is the most recent fiscal year the app reports on. The
// budget page defaults to this when no ?year is provided.
const DefaultBudgetYear = 2026

// SankeyData is the JSON structure passed to the D3 sankey renderer.
type SankeyData struct {
	Title        string                  `json:"title,omitempty"`
	TaxLevy      string                  `json:"taxLevy"`
	IncomeTotal  string                  `json:"incomeTotal"`
	ExpenseTotal string                  `json:"expenseTotal"`
	SourceURL    string                  `json:"sourceURL,omitempty"`
	SourceNote   string                  `json:"sourceNote,omitempty"`
	Nodes        []SankeyNode            `json:"nodes"`
	Links        []SankeyLink            `json:"links"`
	Details      map[string]SankeyDetail `json:"details"`
	LinkDetails  map[string]string       `json:"linkDetails,omitempty"`
}

type SankeyNode struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

type SankeyLink struct {
	Source int     `json:"source"`
	Target int     `json:"target"`
	Value  float64 `json:"value"`
}

type SankeyDetail struct {
	Total       float64 `json:"total"`
	Percent     int     `json:"percent"`
	Color       string  `json:"color"`
	Description string  `json:"description"`
	Change      string  `json:"change,omitempty"`
}

// BudgetItemView is a pre-formatted budget item for the template. Only the
// display fields the ledger can populate are filled — Summary, Highlights,
// Note, NoteRef stay empty (the template's `if len(...) > 0` / `if x != ""`
// checks elide them naturally).
type BudgetItemView struct {
	Name        string
	AmountLabel string
	PctLabel    string
	BarWidth    string
	Color       string
}

// ServiceSankeyJSON is a detail Sankey for a single service, serializable to JSON.
type ServiceSankeyJSON struct {
	Title        string                  `json:"title"`
	Total        float64                 `json:"total"`
	IncomeTotal  string                  `json:"incomeTotal"`
	ExpenseTotal string                  `json:"expenseTotal"`
	Source       string                  `json:"source"`
	SourceNote   string                  `json:"sourceNote,omitempty"`
	Description  string                  `json:"description,omitempty"`
	Nodes        []SankeyNode            `json:"nodes"`
	Links        []SankeyLink            `json:"links"`
	Details      map[string]SankeyDetail `json:"details"`
}

// BudgetTopLine holds the revenue/spending summary for the budget page header.
type BudgetTopLine struct {
	TotalRevenue string
	PropertyTax  string
	TaxPct       string
	CapitalLevy  string
	CapitalNote  string
	Grants       string
	GrantsPct    string
	OtherSources string
	OtherPct     string
	Operating    string
	HasSpending  bool
}

type CapitalBudgetView struct {
	HasData       bool
	TotalLabel    string
	ProjectCount  int
	YearLabels    []CapitalYearTotalView
	FundingLabels []CapitalFundingTotalView
	Projects      []CapitalProjectView
}

type CapitalYearTotalView struct {
	Year        int
	AmountLabel string
	LevyLabel   string
}

type CapitalFundingTotalView struct {
	Year        int
	Kind        string
	Label       string
	Amount      float64
	AmountLabel string
	ShareLabel  string
	SharePct    float64
	BarWidth    string
}

type CapitalProjectView struct {
	ID              string
	Name            string
	OfficialName    string
	Service         string
	Category        string
	AssetType       string
	Action          string
	Status          string
	Description     string
	Benefits        string
	Ward            string
	Location        string
	SourceContext   string
	TotalAmount     float64
	TotalLabel      string
	Years           []CapitalYearTotalView
	Funding         []CapitalFundingView
	SourceLineCount int
	SourceLines     []CapitalSourceLineView
	Approval        string
	SourceURL       string
	SourcePage      int
}

type CapitalSourceLineView struct {
	Category    string
	Amount      float64
	AmountLabel string
}

type CapitalFundingView struct {
	Kind        string
	Label       string
	Source      string
	Amount      float64
	AmountLabel string
	BarWidth    string
}

type capitalFundingTotal struct {
	Kind   string
	Label  string
	Amount float64
}

const (
	capitalBudget2026Total = 159_997_300
	capitalBudget2027Total = 148_044_700
)

var capitalBudgetYearTotals = map[int]float64{
	2026: capitalBudget2026Total,
	2027: capitalBudget2027Total,
}

var capitalBudgetLevyTotals = map[int]float64{
	2026: 23_043_400,
	2027: 24_505_000,
}

var capitalBudgetFundingTotals = map[int][]capitalFundingTotal{
	2026: {
		{Kind: "tax_levy", Label: "Tax Levy", Amount: 23_043_400},
		{Kind: "grant", Label: "Grants", Amount: 56_160_300},
		{Kind: "developer", Label: "Developer Contributions", Amount: 1_030_000},
		{Kind: "debenture", Label: "Debt", Amount: 41_910_500},
		{Kind: "reserve", Label: "Reserves and Reserve Funds", Amount: 37_853_100},
	},
	2027: {
		{Kind: "tax_levy", Label: "Tax Levy", Amount: 24_505_000},
		{Kind: "grant", Label: "Grants", Amount: 48_620_300},
		{Kind: "developer", Label: "Developer Contributions", Amount: 0},
		{Kind: "debenture", Label: "Debt", Amount: 27_454_000},
		{Kind: "reserve", Label: "Reserves and Reserve Funds", Amount: 47_465_400},
	},
}

type operatingFundingSummary struct {
	Total        float64
	Levy         float64
	Grants       float64
	OtherSources float64
}

const (
	operatingBudget2026TaxSupportedTotal  = 412_198_500
	operatingBudget2026RateSupportedTotal = 66_532_200
	operatingBudget2026Total              = operatingBudget2026TaxSupportedTotal + operatingBudget2026RateSupportedTotal
	operatingBudget2026Levy               = 227_388_800
	operatingBudget2026Grants             = 81_578_900
	operatingBudget2026OtherSources       = operatingBudget2026Total - operatingBudget2026Levy - operatingBudget2026Grants
)

var operatingFundingSummaries = map[int]operatingFundingSummary{
	2026: {
		Total:        operatingBudget2026Total,
		Levy:         operatingBudget2026Levy,
		Grants:       operatingBudget2026Grants,
		OtherSources: operatingBudget2026OtherSources,
	},
}

// BudgetViewModel is the data the budget page renders. When HasData is false
// the template renders an empty state — every other field is the zero value.
type BudgetViewModel struct {
	Year           int
	Years          []int
	HasData        bool
	Items          []BudgetItemView
	ItemsDesc      []BudgetItemView
	TopLine        BudgetTopLine
	Sankey         SankeyData
	ServiceDetails map[string]ServiceSankeyJSON
}

// NewBudgetViewModel builds the view model entirely from the ledger. There is
// no fallback to compiled-in seed data — when the ledger has no entries for the
// requested year, HasData is false and the template renders an empty state.
// Returns an error on any DB failure so callers can surface a 503.
func NewBudgetViewModel(year int, ctx context.Context, ledger *budget.Ledger) (BudgetViewModel, error) {
	vm := BudgetViewModel{
		Year:  year,
		Years: []int{DefaultBudgetYear},
	}

	if ledger == nil || ctx == nil {
		return vm, nil
	}

	hasData, err := ledger.HasEntries(ctx, year)
	if err != nil {
		return vm, fmt.Errorf("budget ledger: %w", err)
	}
	if !hasData {
		return vm, nil
	}

	summary, err := ledger.OperatingSummaryForYear(ctx, year)
	if err != nil {
		return vm, fmt.Errorf("budget operating summary: %w", err)
	}

	svcTotals, err := ledger.TotalByService(ctx, year)
	if err != nil {
		return vm, fmt.Errorf("budget service totals: %w", err)
	}

	taxLevyLabel := dollarsToMillionsLabel(summary.PropertyTax)
	topLineSummary := operatingTopLineSummary(year, summary)
	topLineTotalLabel := dollarsToMillionsLabel(topLineSummary.Total)

	sankey, svcDetails, err := BuildSankeyFromLedger(ctx, ledger, year, taxLevyLabel, "", "")
	if err != nil {
		return vm, fmt.Errorf("budget sankey: %w", err)
	}
	annotateCapitalBudgetSankeyDetail(ctx, ledger, &sankey, svcDetails)
	// Build per-service items, ascending by amount (small first → big at the
	// bottom of the Sankey list, matching the renderer's row order).
	sort.SliceStable(svcTotals, func(i, j int) bool {
		return svcTotals[i].Total < svcTotals[j].Total
	})

	items := make([]BudgetItemView, 0, len(svcTotals))
	maxAmount := 0.0
	for _, t := range svcTotals {
		if t.Total > maxAmount {
			maxAmount = t.Total
		}
	}
	for _, t := range svcTotals {
		amountM := t.Total / 1_000_000
		pct := 0
		if summary.TotalExpenditure > 0 {
			pct = int(t.Total / summary.TotalExpenditure * 100)
		}
		bar := 4
		if maxAmount > 0 {
			bar = int(t.Total / maxAmount * 100)
			if bar < 4 {
				bar = 4
			}
		}
		items = append(items, BudgetItemView{
			Name:        t.Name,
			AmountLabel: fmt.Sprintf("$%.1fM", amountM),
			PctLabel:    fmt.Sprintf("%d%%", pct),
			BarWidth:    fmt.Sprintf("%d", bar),
			Color:       t.Color,
		})
	}

	itemsDesc := make([]BudgetItemView, len(items))
	copy(itemsDesc, items)
	for i, j := 0, len(itemsDesc)-1; i < j; i, j = i+1, j-1 {
		itemsDesc[i], itemsDesc[j] = itemsDesc[j], itemsDesc[i]
	}

	vm.HasData = true
	vm.Items = items
	vm.ItemsDesc = itemsDesc
	vm.TopLine = BudgetTopLine{
		TotalRevenue: topLineTotalLabel,
		PropertyTax:  dollarsToMillionsLabel(topLineSummary.Levy),
		TaxPct:       pctOfTotalLabel(topLineSummary.Levy, topLineSummary.Total),
		Grants:       dollarsToMillionsLabel(topLineSummary.Grants),
		GrantsPct:    pctOfTotalLabel(topLineSummary.Grants, topLineSummary.Total),
		OtherSources: dollarsToMillionsLabel(topLineSummary.OtherSources),
		OtherPct:     pctOfTotalLabel(topLineSummary.OtherSources, topLineSummary.Total),
		Operating:    topLineTotalLabel,
		HasSpending:  true,
	}
	if capitalLevy, ok := capitalBudgetLevyTotals[year]; ok && capitalLevy > 0 {
		vm.TopLine.CapitalLevy = dollarsToMillionsLabel(capitalLevy)
		vm.TopLine.CapitalNote = "separate from operating"
	}
	vm.Sankey = sankey
	vm.ServiceDetails = svcDetails
	return vm, nil
}

func operatingTopLineSummary(year int, summary *budget.OperatingSummary) operatingFundingSummary {
	if official, ok := operatingFundingSummaries[year]; ok {
		return official
	}
	if summary == nil {
		return operatingFundingSummary{}
	}
	return operatingFundingSummary{
		Total:        summary.TotalExpenditure,
		Levy:         summary.PropertyTax,
		Grants:       summary.Grants,
		OtherSources: summary.OtherRevenue,
	}
}

func annotateCapitalBudgetSankeyDetail(ctx context.Context, ledger *budget.Ledger, sankey *SankeyData, serviceDetails map[string]ServiceSankeyJSON) {
	if sankey == nil || ledger == nil {
		return
	}
	detail, ok := sankey.Details["Capital Budget"]
	if !ok {
		return
	}
	projects, err := ledger.CapitalProjects(ctx, 2026, 2027)
	if err != nil || len(projects) == 0 {
		return
	}
	description := capitalBudgetSankeyDescription(len(projects))
	detail.Description = description
	sankey.Details["Capital Budget"] = detail
	if serviceDetail, ok := serviceDetails["Capital Budget"]; ok {
		serviceDetail.Description = description
		serviceDetails["Capital Budget"] = serviceDetail
	}
}

func capitalBudgetSankeyDescription(projectCount int) string {
	return fmt.Sprintf("This is the 2026 tax-supported contribution into the separate 2026-2027 capital plan. The capital page tracks %d approved project lines, including roads, bridges, water and sewer work, stormwater, transit, parks and facilities, fleet, emergency services, and digital planning.", projectCount)
}

func NewCapitalBudgetView(ctx context.Context, ledger *budget.Ledger) (CapitalBudgetView, error) {
	projects, err := ledger.CapitalProjects(ctx, 2026, 2027)
	if err != nil {
		return CapitalBudgetView{}, err
	}
	if len(projects) == 0 {
		return CapitalBudgetView{}, nil
	}

	vm := CapitalBudgetView{HasData: true, ProjectCount: len(projects)}
	vm.TotalLabel = dollarsToMillionsLabel(capitalBudget2026Total + capitalBudget2027Total)
	for _, y := range sortedCapitalYears(capitalBudgetYearTotals) {
		vm.YearLabels = append(vm.YearLabels, CapitalYearTotalView{
			Year:        y,
			AmountLabel: dollarsToMillionsLabel(capitalBudgetYearTotals[y]),
			LevyLabel:   dollarsToMillionsLabel(capitalBudgetLevyTotals[y]),
		})
	}
	for _, y := range sortedCapitalYears(capitalBudgetYearTotals) {
		vm.FundingLabels = append(vm.FundingLabels, capitalFundingTotals(y, capitalBudgetFundingTotals[y], capitalBudgetYearTotals[y])...)
	}

	sourceLineGroups := capitalProjectSourceLineGroups(projects)
	for _, p := range projects {
		card := CapitalProjectView{
			ID:            p.ID,
			Name:          capitalProjectDisplayName(p.Name, p.OfficialName),
			OfficialName:  p.OfficialName,
			Service:       p.Service,
			Category:      p.Category,
			AssetType:     p.AssetType,
			Action:        p.Action,
			Status:        strings.ToUpper(p.Status),
			Description:   p.Description,
			Benefits:      p.Benefits,
			Ward:          p.Ward,
			Location:      p.Location,
			SourceContext: p.SourceContext,
			SourceURL:     p.SourceURL,
			SourcePage:    p.SourcePage,
		}
		var projectTotal, maxFunding float64
		for _, y := range p.Years {
			projectTotal += y.Amount
			card.Years = append(card.Years, CapitalYearTotalView{
				Year:        y.FiscalYear,
				AmountLabel: capitalProjectAmountLabel(y.Amount),
			})
		}
		for _, f := range p.Funding {
			if f.Amount > maxFunding {
				maxFunding = f.Amount
			}
		}
		for _, f := range p.Funding {
			width := 4
			if maxFunding > 0 {
				width = int(f.Amount / maxFunding * 100)
				if width < 4 {
					width = 4
				}
			}
			card.Funding = append(card.Funding, CapitalFundingView{
				Kind:        f.Kind,
				Label:       fundingKindLabel(f.Kind),
				Source:      f.Source,
				Amount:      f.Amount,
				AmountLabel: capitalProjectAmountLabel(f.Amount),
				BarWidth:    fmt.Sprintf("%d", width),
			})
		}
		if len(p.Approvals) > 0 {
			a := p.Approvals[0]
			card.Approval = strings.TrimSpace(fmt.Sprintf("%s · %s · %s", a.Date, a.Body, a.Result))
		}
		card.TotalAmount = projectTotal
		card.TotalLabel = capitalProjectAmountLabel(projectTotal)
		if sourceLines := sourceLineGroups[capitalProjectOfficialNumber(p.ID)]; sourceLines.Count > 1 {
			card.SourceLineCount = sourceLines.Count
			card.SourceLines = sourceLines.Lines
		}
		vm.Projects = append(vm.Projects, card)
	}
	sortCapitalProjectsByTotalDesc(vm.Projects)

	return vm, nil
}

func sortCapitalProjectsByTotalDesc(projects []CapitalProjectView) {
	sort.SliceStable(projects, func(i, j int) bool {
		if projects[i].TotalAmount == projects[j].TotalAmount {
			return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
		}
		return projects[i].TotalAmount > projects[j].TotalAmount
	})
}

func capitalProjectDisplayName(name, officialName string) string {
	if strings.TrimSpace(officialName) != "" {
		return officialName
	}
	return name
}

type capitalProjectSourceLineGroup struct {
	Count int
	Lines []CapitalSourceLineView
}

func capitalProjectSourceLineGroups(projects []budget.CapitalProject) map[string]capitalProjectSourceLineGroup {
	grouped := map[string][]budget.CapitalProject{}
	for _, p := range projects {
		number := capitalProjectOfficialNumber(p.ID)
		if number == "" {
			continue
		}
		grouped[number] = append(grouped[number], p)
	}

	result := map[string]capitalProjectSourceLineGroup{}
	for number, rows := range grouped {
		if len(rows) <= 1 {
			continue
		}
		byLabel := map[string]float64{}
		for _, p := range rows {
			label := capitalProjectSourceLineLabel(p, rows)
			byLabel[label] += capitalProjectSourceTotal(p)
		}
		lines := make([]CapitalSourceLineView, 0, len(byLabel))
		for label, amount := range byLabel {
			lines = append(lines, CapitalSourceLineView{
				Category:    label,
				Amount:      amount,
				AmountLabel: capitalProjectAmountLabel(amount),
			})
		}
		sort.SliceStable(lines, func(i, j int) bool {
			if lines[i].Amount == lines[j].Amount {
				return lines[i].Category < lines[j].Category
			}
			return lines[i].Amount > lines[j].Amount
		})
		result[number] = capitalProjectSourceLineGroup{Count: len(rows), Lines: lines}
	}
	return result
}

func capitalProjectSourceLineLabel(p budget.CapitalProject, siblings []budget.CapitalProject) string {
	for _, labeler := range []func(budget.CapitalProject) string{
		func(p budget.CapitalProject) string { return strings.TrimSpace(p.Category) },
		func(p budget.CapitalProject) string { return strings.TrimSpace(p.Service) },
		func(p budget.CapitalProject) string {
			return strings.TrimSpace(capitalProjectDisplayName(p.Name, p.OfficialName))
		},
	} {
		if distinctCapitalProjectSourceLabels(siblings, labeler) > 1 {
			if label := labeler(p); label != "" {
				return label
			}
		}
	}
	return "Budget line"
}

func distinctCapitalProjectSourceLabels(projects []budget.CapitalProject, labeler func(budget.CapitalProject) string) int {
	seen := map[string]bool{}
	for _, p := range projects {
		if label := labeler(p); label != "" {
			seen[label] = true
		}
	}
	return len(seen)
}

func capitalProjectSourceTotal(p budget.CapitalProject) float64 {
	var total float64
	for _, y := range p.Years {
		total += y.Amount
	}
	return total
}

func capitalProjectOfficialNumber(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	parts := strings.Split(id, "-")
	limit := 4
	if len(parts) < limit {
		limit = len(parts)
	}
	if len(parts) > limit && allDigits(parts[limit]) {
		limit++
	}
	return strings.ToUpper(strings.Join(parts[:limit], "-"))
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func capitalFundingTotals(year int, totals []capitalFundingTotal, grand float64) []CapitalFundingTotalView {
	var maxTotal float64
	for _, t := range totals {
		if t.Amount > maxTotal {
			maxTotal = t.Amount
		}
	}

	labels := make([]CapitalFundingTotalView, 0, len(totals))
	for _, t := range totals {
		width := 4
		if maxTotal > 0 {
			width = int(t.Amount / maxTotal * 100)
			if width < 4 {
				width = 4
			}
		}
		share := 0.0
		if grand > 0 {
			share = t.Amount / grand * 100
		}
		labels = append(labels, CapitalFundingTotalView{
			Year:        year,
			Kind:        t.Kind,
			Label:       t.Label,
			Amount:      t.Amount,
			AmountLabel: dollarsToMillionsLabelWithZero(t.Amount),
			ShareLabel:  fmt.Sprintf("%.0f%%", share),
			SharePct:    share,
			BarWidth:    fmt.Sprintf("%d", width),
		})
	}
	return labels
}

func sortedCapitalYears(totals map[int]float64) []int {
	years := make([]int, 0, len(totals))
	for y := range totals {
		years = append(years, y)
	}
	sort.Ints(years)
	return years
}

func fundingKindLabel(kind string) string {
	switch kind {
	case "tax_levy":
		return "Tax Levy"
	case "grant_federal":
		return "Federal Grants"
	case "grant_provincial":
		return "Provincial Grants"
	case "grant_joint":
		return "Fed/Prov Grants"
	case "grant":
		return "Grants"
	case "reserve":
		return "Reserves"
	case "debenture":
		return "Debenture"
	case "internal_loan":
		return "Internal Loan"
	case "developer":
		return "Developer"
	case "rate":
		return "Rate"
	default:
		return "Other"
	}
}

// dollarsToMillionsLabel formats a dollar amount as "$X.XM" suitable for the
// header. Returns "" for non-positive amounts so the template can hide them.
func dollarsToMillionsLabel(dollars float64) string {
	if dollars <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.1fM", dollars/1_000_000)
}

func dollarsToMillionsLabelWithZero(dollars float64) string {
	if dollars <= 0 {
		return "$0"
	}
	return dollarsToMillionsLabel(dollars)
}

func capitalProjectAmountLabel(dollars float64) string {
	if dollars <= 0 {
		return "$0"
	}
	if dollars < 1_000 {
		return fmt.Sprintf("$%.0f", dollars)
	}
	if dollars < 1_000_000 {
		thousands := dollars / 1_000
		if thousands < 10 {
			return fmt.Sprintf("$%.1fK", thousands)
		}
		return fmt.Sprintf("$%.0fK", thousands)
	}
	return dollarsToMillionsLabel(dollars)
}

func pctLabel(part, total float64) string {
	if total <= 0 || part <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f%%", part/total*100)
}

func pctOfTotalLabel(part, total float64) string {
	if total <= 0 || part <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f%% of total revenue", part/total*100)
}
