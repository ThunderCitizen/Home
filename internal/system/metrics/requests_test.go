package metrics_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"thundercitizen/internal/middleware"
	"thundercitizen/internal/system/metrics"
)

type streamingWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *streamingWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestMiddlewarePreservesStreaming(t *testing.T) {
	for _, env := range []string{"production", "development"} {
		t.Run(env, func(t *testing.T) {
			w := &streamingWriter{ResponseRecorder: httptest.NewRecorder(), deadline: time.Now()}
			r := chi.NewRouter()
			r.Use(metrics.Middleware)
			r.Use(middleware.RequestLogger)
			r.Use(middleware.NoCacheInDev(env))
			r.Use(middleware.PageUnavailable(func(context.Context, io.Writer) error { return nil }))
			r.Get("/api/transit/vehicles/stream", func(rw http.ResponseWriter, req *http.Request) {
				if err := http.NewResponseController(rw).SetWriteDeadline(time.Time{}); err != nil {
					t.Errorf("clear stream write deadline: %v", err)
				}
				flusher, ok := rw.(http.Flusher)
				if !ok {
					t.Fatal("stream writer does not implement http.Flusher")
				}
				_, _ = io.WriteString(rw, "data: {}\n\n")
				flusher.Flush()
				if !w.Flushed {
					t.Error("event was not flushed before handler returned")
				}
			})
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/transit/vehicles/stream", nil))
			if !w.deadline.IsZero() {
				t.Error("stream write deadline was not cleared")
			}
		})
	}
}
