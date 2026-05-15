package views

import (
	"fmt"
	"time"

	"thundercitizen/internal/transit"
)

// AboutViewModel carries the data-freshness report shown on /about in place of
// the old PageSpeed certificate.
type AboutViewModel struct {
	Freshness FreshnessReport
}

// FreshnessReport holds the rows of the freshness card. Each row gets an age
// label ("12s ago") and a status ("fresh"/"stale"/"unknown") that drives the
// pill color. CheckedAt is shown as the absolute timestamp for hover/tooltip.
type FreshnessReport struct {
	GeneratedAt string
	Rows        []FreshnessRow
}

type FreshnessRow struct {
	Source    string // "Transit · Vehicle positions"
	Detail    string // "GTFS-RT feed"
	DetailURL string // optional — when set, Detail renders as a link
	Expected  string // "every 6s" | "when published"
	Age       string // "12s ago"
	When      string // "Apr 19, 2026 7:43 AM"
	Note      string // optional caveat shown beneath the row
}

// NewAboutViewModel builds the freshness card from the recorder snapshot, the
// most recent council-minutes scrape, and the most recent muni bundle check
// (which gates budget and councillor data updates).
func NewAboutViewModel(now time.Time, feeds []transit.FeedStats, lastMinutesCheck, lastBundleCheck time.Time) AboutViewModel {
	rows := make([]FreshnessRow, 0, len(feeds)+2)

	budgetRow := freshnessRow(
		"Budget · Operating ledger",
		"2026 Operating Budget",
		"annual",
		lastBundleCheck,
		now,
	)
	budgetRow.DetailURL = "https://thunderbay.ca/en/city-hall/2026-budget.aspx"
	rows = append(rows, budgetRow)

	const gtfsOpenDataURL = "https://www.thunderbay.ca/en/city-services/developers---open-data.aspx"
	for _, f := range feeds {
		label, detail := transitFeedLabel(f.FeedType)
		row := freshnessRow(label, detail, "every "+humanInterval(f.Interval), f.LastSuccessAt, now)
		row.DetailURL = gtfsOpenDataURL
		rows = append(rows, row)
	}

	minutesRow := freshnessRow(
		"Council minutes",
		"eScribe portal",
		"when published",
		lastMinutesCheck,
		now,
	)
	minutesRow.DetailURL = "https://pub-thunderbay.escribemeetings.com"
	minutesRow.Note = "We check eScribe regularly. Meetings are listed there promptly, but no official minutes PDFs have been posted since April 7."
	rows = append(rows, minutesRow)

	return AboutViewModel{
		Freshness: FreshnessReport{
			GeneratedAt: now.Local().Format("Jan 2, 2006 3:04 PM"),
			Rows:        rows,
		},
	}
}

func transitFeedLabel(feedType string) (string, string) {
	switch feedType {
	case "vehicles":
		return "Transit · Vehicle positions", "GTFS-RT feed"
	case "trips":
		return "Transit · Trip updates", "GTFS-RT feed"
	case "alerts":
		return "Transit · Service alerts", "GTFS-RT feed"
	}
	return "Transit · " + feedType, "GTFS-RT feed"
}

func freshnessRow(label, detail, expected string, last time.Time, now time.Time) FreshnessRow {
	if last.IsZero() {
		return FreshnessRow{
			Source:   label,
			Detail:   detail,
			Expected: expected,
			Age:      "—",
			When:     "no successful fetch yet",
		}
	}
	return FreshnessRow{
		Source:   label,
		Detail:   detail,
		Expected: expected,
		Age:      humanAge(now.Sub(last)) + " ago",
		When:     last.Local().Format("Jan 2, 2006 3:04 PM"),
	}
}

func humanInterval(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func humanAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
