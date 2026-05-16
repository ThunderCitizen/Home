package analytics

import (
	"net"
	"net/http"
	"strings"
)

// Track records GET pageviews for human-facing HTML pages. It skips API,
// static, and health/version routes plus non-200s and non-GET requests.
// Bot filtering is deliberately NOT done here — GoatCounter classifies
// bots itself, excludes them from counts by default, and keeps them so
// "human vs bot" stays auditable in the dashboard. Pre-filtering would
// just throw that evidence away. Everything is fire-and-forget via the
// client's bounded queue.
func Track(c *Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !c.enabled || r.Method != http.MethodGet || !trackablePath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			if sw.status >= 400 {
				return
			}
			// No Ref: /about promises the referer is not logged. Keep
			// that true — referer is optional for GoatCounter.
			c.Enqueue(hit{
				Path:      r.URL.Path,
				UserAgent: r.UserAgent(),
				IP:        clientIP(r),
			})
		})
	}
}

func trackablePath(p string) bool {
	if p == "/health" || p == "/version" {
		return false
	}
	if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/static/") {
		return false
	}
	return true
}

// clientIP returns the real visitor IP. Caddy sets X-Forwarded-For on
// reverse_proxy app:8080; the first hop is the original client.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// statusWriter captures the response status. Mirrors the unexported one in
// internal/middleware; kept local so this package has no cross-dependency.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
