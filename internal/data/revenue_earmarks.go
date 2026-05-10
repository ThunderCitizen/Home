package data

// RevenueEarmarks maps a revenue code to the sub-account it funds directly.
// These revenues flow 100% to their target rather than being split
// proportionally across services. Used by the budget Sankey ledger view.
var RevenueEarmarks = map[string]string{
	"revenue.housing_rents": "service.tbdssab_levy.community_housing_homelessness",
}
