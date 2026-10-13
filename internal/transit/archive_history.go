package transit

import (
	"fmt"
	"net/http"
	"time"

	"thundercitizen/internal/cache"
	"thundercitizen/internal/httperr"
)

// archivedHistory stops raw-detail views once the verified release is published,
// before the worker begins removing raw partitions. Retained summaries remain
// available throughout closure.
func (h *Handler) archivedHistory(w http.ResponseWriter, r *http.Request, date time.Time) bool {
	// Handler tests may provide timetable loaders without a database.
	if h.svc.db == nil {
		return false
	}
	var archived bool
	err := h.svc.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM transit.archive_month WHERE month=date_trunc('month',$1::date)::date AND state IN ('published','complete'))`, date).Scan(&archived)
	if err != nil {
		httperr.Internal(w, err)
		return true
	}
	if !archived {
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cache.Live)
	w.WriteHeader(http.StatusGone)
	_, _ = fmt.Fprintf(w, `<p>Detailed observations for this service month have been archived. <a href="/transit/routes?date=%s">View daily route summaries</a> or <a href="/transit/method">download consolidated reports</a>.</p>`, date.Format("2006-01-02"))
	return true
}
