package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDisabledClientIsNoOp(t *testing.T) {
	c := New("", "", "analytics.local") // no url/token ⇒ disabled
	if c.enabled {
		t.Fatal("client should be disabled with empty url/token")
	}
	c.Enqueue(hit{Path: "/x"})  // must not panic or block
	c.Run(context.Background()) // returns immediately
	c.Wait()                    // returns immediately
}

func TestTrackFiltersAndReports(t *testing.T) {
	var (
		mu   sync.Mutex
		got  []hit
		host string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		host = r.Host
		var body countBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body.Hits...)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "analytics.local")
	if !c.enabled {
		t.Fatal("client should be enabled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	h := Track(c)(next)

	cases := []struct {
		method, path, ua, xff string
		tracked               bool
	}{
		{"GET", "/transit", "Mozilla/5.0", "203.0.113.7", true},
		{"GET", "/budget", "curl/8.1", "", true},            // bots NOT pre-filtered — GoatCounter classifies them
		{"GET", "/api/transit/x", "Mozilla/5.0", "", false}, // api
		{"GET", "/static/css/x", "Mozilla/5.0", "", false},  // static
		{"GET", "/health", "Mozilla/5.0", "", false},        // health
		{"POST", "/budget", "Mozilla/5.0", "", false},       // non-GET
		{"GET", "/boom", "Mozilla/5.0", "", false},          // 5xx
	}
	want := map[string]bool{}
	for _, tc := range cases {
		if tc.tracked {
			want[tc.path] = true
		}
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("User-Agent", tc.ua)
		if tc.xff != "" {
			req.Header.Set("X-Forwarded-For", tc.xff+", 10.0.0.1")
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	cancel()
	c.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("want %d tracked hits, got %d: %+v", len(want), len(got), got)
	}
	for _, h := range got {
		if !want[h.Path] {
			t.Fatalf("unexpected tracked path %q", h.Path)
		}
		if h.Path == "/transit" && h.IP != "203.0.113.7" {
			t.Fatalf("want IP from XFF, got %q", h.IP)
		}
	}
	if host != "analytics.local" {
		t.Fatalf("want Host analytics.local, got %q", host)
	}
}

func TestQueueFullDropsWithoutBlocking(t *testing.T) {
	c := New("http://127.0.0.1:1", "tok", "analytics.local") // never drained
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueCap*2; i++ {
			c.Enqueue(hit{Path: "/x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue blocked on a full queue")
	}
}
