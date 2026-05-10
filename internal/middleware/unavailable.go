package middleware

import (
	"context"
	"io"
	"net/http"
	"strings"

	"thundercitizen/internal/httperr"
	"thundercitizen/internal/logger"
)

// errCarrierKey is the context key for the mutable error carrier.
type errCarrierKey struct{}

// errCarrier is a mutable slot written by handlers and read by the
// PageUnavailable middleware when it intercepts a 503.
type errCarrier struct {
	err error
}

// RecordError attaches err to the request context so the PageUnavailable
// middleware can log it once at the point the 503 is intercepted. Handlers
// should call this before emitting httperr.Unavailable on page routes.
// Safe to call when the context has no carrier (no-op).
func RecordError(ctx context.Context, err error) {
	if ec, ok := ctx.Value(errCarrierKey{}).(*errCarrier); ok {
		ec.err = err
	}
}

// HandleUnavailable records err on the request context (so PageUnavailable
// can render the diagnostic) and writes a 503 response. Use at every
// page-handler boundary where a downstream dep failed — this is the
// canonical pairing of RecordError + httperr.Unavailable.
func HandleUnavailable(ctx context.Context, w http.ResponseWriter, msg string, err error) {
	RecordError(ctx, err)
	httperr.Unavailable(w, msg)
}

// PageUnavailable intercepts HTTP 503 responses on HTML page routes and
// replaces the JSON error body with a themed error page. The single log line
// it emits (carrying req_id) is the authoritative record of the failure —
// callers should not log separately.
//
// API routes (/api/), static files (/static/), and utility endpoints
// (/health, /version) are passed through unchanged so JSON clients keep
// receiving JSON.
//
// render is called to write the HTML page body after the 503 status is sent.
func PageUnavailable(render func(ctx context.Context, w io.Writer) error) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipIntercept(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			ec := &errCarrier{}
			r = r.WithContext(context.WithValue(r.Context(), errCarrierKey{}, ec))

			iw := &unavailWriter{ResponseWriter: w, req: r, render: render, ec: ec}
			next.ServeHTTP(iw, r)
		})
	}
}

func skipIntercept(path string) bool {
	return strings.HasPrefix(path, "/api/") ||
		strings.HasPrefix(path, "/static/") ||
		path == "/health" ||
		path == "/version"
}

// unavailWriter intercepts WriteHeader(503) and replaces the response.
type unavailWriter struct {
	http.ResponseWriter
	req      *http.Request
	render   func(ctx context.Context, w io.Writer) error
	ec       *errCarrier
	hijacked bool
}

func (w *unavailWriter) WriteHeader(code int) {
	if code == http.StatusServiceUnavailable && !w.hijacked {
		w.hijacked = true
		l := logger.FromContext(w.req.Context())
		if w.ec.err != nil {
			l.Error("service unavailable", "path", w.req.URL.Path, "err", w.ec.err)
		} else {
			l.Warn("service unavailable", "path", w.req.URL.Path)
		}
		h := w.ResponseWriter.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.ResponseWriter.WriteHeader(code)
		w.render(w.req.Context(), w.ResponseWriter)
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *unavailWriter) Write(b []byte) (int, error) {
	if w.hijacked {
		return len(b), nil // discard JSON body; HTML already written in WriteHeader
	}
	return w.ResponseWriter.Write(b)
}

func (w *unavailWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *unavailWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
