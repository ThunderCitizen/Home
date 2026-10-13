package transit

import (
	"net/http"
	"time"
)

// parseRouteDay selects one local service day. Today remains available as a
// provisional summary; older days are served from the same retained chunks.
func parseRouteDay(r *http.Request, since string) DateRange {
	latest := ServiceDate()
	earliest := mustParseDate(since, latest)
	if earliest.After(latest) {
		earliest = latest
	}
	date := latest
	for _, key := range []string{"date", "from", "to"} {
		if parsed, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get(key), TZ); err == nil {
			date = parsed
			break
		}
	}
	if r.URL.Query().Get("date") == "" && r.URL.Query().Get("from") == "" && r.URL.Query().Get("to") == "" {
		if parsed, err := time.ParseInLocation("2006-01", r.URL.Query().Get("month"), TZ); err == nil {
			date = parsed
		}
	}
	if date.Before(earliest) {
		date = earliest
	}
	if date.After(latest) {
		date = latest
	}
	return DateRange{From: date.Format("2006-01-02"), To: date.Format("2006-01-02"), MinDate: earliest.Format("2006-01-02"), MaxDate: latest.Format("2006-01-02")}
}
