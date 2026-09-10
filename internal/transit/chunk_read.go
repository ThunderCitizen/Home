package transit

import (
	"context"
	"time"

	"thundercitizen/internal/transit/chunk"
)

// Chunks reads the precomputed metric rows for [from, to] inclusive. The
// date index bounds the query; counts and sums are aggregated by the caller.
// Reading the rollup table directly makes backfills and corrections visible
// on the next request, including dates that previously had no rows.
func (r *Repo) Chunks(ctx context.Context, from, to time.Time) ([]chunk.ChunkView, error) {
	from = DateOnly(from)
	to = DateOnly(to)
	if to.Before(from) {
		return nil, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT route_id, date, band, service_kind,
		       trip_count, on_time_count,
		       scheduled_count, cancelled_count, no_notice_count,
		       headway_count, headway_sum_sec, headway_sum_sec_sq, sched_headway_sec,
		       built_at
		FROM transit.route_band_chunk
		WHERE date >= $1::date AND date <= $2::date
		ORDER BY date,
			CASE band WHEN 'morning' THEN 0 WHEN 'midday' THEN 1 WHEN 'evening' THEN 2 ELSE 3 END,
			route_id COLLATE "C"
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]chunk.ChunkView, 0, 64)
	for rows.Next() {
		var ck chunk.Chunk
		if err := rows.Scan(
			&ck.RouteID, &ck.Date, &ck.Band, &ck.ServiceKind,
			&ck.TripCount, &ck.OnTimeCount,
			&ck.ScheduledCount, &ck.CancelledCount, &ck.NoNoticeCount,
			&ck.HeadwayCount, &ck.HeadwaySumSec, &ck.HeadwaySumSecSq, &ck.SchedHeadwaySec,
			&ck.BuiltAt,
		); err != nil {
			return nil, err
		}
		out = append(out, ck.View())
	}
	return out, rows.Err()
}

// EarliestChunkDate returns zero when the rollup table is empty. Its date
// index finds the first row without scanning history; subsequent reads see
// newly inserted or backfilled dates without keeping cache state.
func (r *Repo) EarliestChunkDate(ctx context.Context) (time.Time, error) {
	var earliest *time.Time
	if err := r.db.QueryRow(ctx, `SELECT MIN(date) FROM transit.route_band_chunk`).Scan(&earliest); err != nil {
		return time.Time{}, err
	}
	if earliest == nil {
		return time.Time{}, nil
	}
	return *earliest, nil
}
