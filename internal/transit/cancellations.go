package transit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CancelDetail is one cancelled trip in a date range — the unit of work
// for the cancel log on the metrics tab. Distinct from CancelledTrip
// (queries.go), which groups the selected service day's recorded cancellations
// for the live page.
type CancelDetail struct {
	Date      string `json:"date"` // YYYY-MM-DD
	RouteID   string `json:"route_id"`
	TripID    string `json:"trip_id"`
	StartTime string `json:"start_time"` // HH:MM (scheduled departure)
	EndTime   string `json:"end_time"`   // HH:MM (scheduled last arrival)
	Headsign  string `json:"headsign"`
	FirstSeen string `json:"first_seen"` // HH:MM in Thunder Bay tz
	LeadMin   int    `json:"lead_min"`   // negative = reported after departure
	LeadLabel string `json:"lead_label"`
}

// LoadCancelDetails uses the same service-date schedule and trip population as
// the chunks. Feed-date filtering would lose advance reports at month boundaries.
func LoadCancelDetails(ctx context.Context, db *pgxpool.Pool, from, to time.Time) ([]CancelDetail, error) {
	rows, err := db.Query(ctx, `WITH reported AS MATERIALIZED (
        SELECT trip_id,start_date,MAX(COALESCE(headsign,'')) AS headsign,MIN(feed_timestamp) AS first_seen
        FROM transit.cancellation
        WHERE start_date BETWEEN to_char($1::date,'YYYYMMDD') AND to_char($2::date,'YYYYMMDD')
        GROUP BY trip_id,start_date
    )
    SELECT r.date,t.route_id,t.trip_id,t.first_departure,
        (t.stops->-1->>'arrival')::int,c.headsign,c.first_seen
        FROM transit.metric_rebuild r
        JOIN transit.metric_trip t ON t.schedule_id=r.schedule_id
        JOIN transit.metric_service s ON s.schedule_id=t.schedule_id AND s.service_id=t.service_id AND s.date=r.date
        JOIN reported c ON c.trip_id=t.trip_id AND c.start_date=to_char(r.date,'YYYYMMDD')
        WHERE r.date BETWEEN $1::date AND $2::date AND t.first_departure>=21600 AND t.first_departure<86400
        ORDER BY r.date,t.first_departure,t.trip_id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CancelDetail
	for rows.Next() {
		var cd CancelDetail
		var date, first time.Time
		var start, end int
		if err := rows.Scan(&date, &cd.RouteID, &cd.TripID, &start, &end, &cd.Headsign, &first); err != nil {
			return nil, err
		}
		cd.Date = date.Format("2006-01-02")
		cd.StartTime = fmt.Sprintf("%02d:%02d", start/3600, start%3600/60)
		cd.EndTime = fmt.Sprintf("%02d:%02d", end/3600, end%3600/60)
		cd.FirstSeen = first.In(TZ).Format("15:04")
		cd.LeadMin = int(metricTimeOrigin(date).Add(time.Duration(start) * time.Second).Sub(first).Minutes())
		cd.LeadLabel = leadLabel(cd.LeadMin)
		out = append(out, cd)
	}
	return out, rows.Err()
}
