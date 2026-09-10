package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBenchTimesCompleteResponses(t *testing.T) {
	for _, n := range []int{1, 3} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			const bodyDelay = 60 * time.Millisecond
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request := requests.Add(1)
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				if request == 1 {
					time.Sleep(bodyDelay)
				}
				io.WriteString(w, "complete body")
			}))
			defer server.Close()

			got := bench(server.URL, route{Path: "/"}, n)
			if got.Error != "" || got.Code != http.StatusOK {
				t.Fatalf("benchmark failed: %+v", got)
			}
			if int(requests.Load()) != n {
				t.Fatalf("sent %d requests, want %d", requests.Load(), n)
			}
			if got.Max < bodyDelay.Milliseconds() || got.Avg < bodyDelay.Milliseconds()/int64(n) {
				t.Errorf("timings must include the first response's body delay: %+v", got.stats)
			}
		})
	}
}

func TestBenchRejectsNonSuccessStatus(t *testing.T) {
	for _, code := range []int{http.StatusFound, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(code)
				io.WriteString(w, "error body")
			}))
			defer server.Close()

			got := bench(server.URL, route{Path: "/"}, 3)
			if !strings.Contains(got.Error, strconv.Itoa(code)) {
				t.Fatalf("expected HTTP %d error, got %q", code, got.Error)
			}
			if got.Code != code || requests.Load() != 1 {
				t.Fatalf("failed response counted as success or retried: %+v, requests=%d", got, requests.Load())
			}
		})
	}
}

func TestBenchRejectsIncompleteResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		io.WriteString(w, "short")
	}))
	defer server.Close()

	got := bench(server.URL, route{Path: "/"}, 3)
	if !strings.Contains(got.Error, io.ErrUnexpectedEOF.Error()) {
		t.Fatalf("incomplete body must fail instead of recording fast headers: %+v", got)
	}
}
