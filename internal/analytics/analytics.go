// Package analytics reports pageviews to a self-hosted GoatCounter instance
// from server-side middleware — no client JavaScript, no cookies. The app
// passes the visitor IP + User-Agent so GoatCounter can compute its own
// daily-salted session hash and count unique visitors; we never store or
// log the raw values ourselves.
//
// The client is deliberately best-effort: a full buffer drops the hit
// rather than blocking or failing the request. Analytics must never slow
// down or break a page.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"thundercitizen/internal/logger"
)

var log = logger.New("analytics")

const (
	// queueCap bounds memory and is the backpressure point — past this we
	// drop hits instead of blocking handlers.
	queueCap = 1024
	// batchMax caps one POST body. GoatCounter allows batching and rate-
	// limits /api/v0/count at 4 req/s, so we send few large batches.
	batchMax = 100
	// flushInterval bounds how long a hit waits before being sent even if
	// the batch never fills.
	flushInterval = 5 * time.Second
)

// hit is one pageview. Field names match GoatCounter's APICountRequestHit.
// We deliberately omit ref (referer) and title — /about promises the
// referer is not logged, and path alone is enough for visit counts.
type hit struct {
	Path      string `json:"path"`
	UserAgent string `json:"user_agent"`
	IP        string `json:"ip"`
}

type countBody struct {
	NoSessions bool  `json:"no_sessions"`
	Hits       []hit `json:"hits"`
}

// Client batches and posts pageviews. A Client whose url or token is empty
// is disabled: Enqueue is a no-op and Run returns immediately. This is the
// dev/local default — zero config, zero behavior change.
type Client struct {
	endpoint string // {url}/api/v0/count
	token    string
	site     string // Host header GoatCounter matches the site on
	enabled  bool

	ch   chan hit
	http *http.Client
	done chan struct{}
}

// New builds a client. site is the vhost GoatCounter matches its single
// site on (default handled by the caller via config).
func New(url, token, site string) *Client {
	c := &Client{
		ch:   make(chan hit, queueCap),
		done: make(chan struct{}),
		http: &http.Client{Timeout: 10 * time.Second},
	}
	if url == "" || token == "" {
		log.Info("analytics disabled (no GOATCOUNTER_URL/TOKEN)")
		close(c.done)
		return c
	}
	c.endpoint = url + "/api/v0/count"
	c.token = token
	c.site = site
	c.enabled = true
	log.Info("analytics enabled", "endpoint", c.endpoint, "site", site)
	return c
}

// Enqueue submits a hit without blocking. A full queue drops it.
func (c *Client) Enqueue(h hit) {
	if !c.enabled {
		return
	}
	select {
	case c.ch <- h:
	default:
		log.Debug("analytics queue full, dropping hit", "path", h.Path)
	}
}

// Run drains the queue until ctx is cancelled, then flushes whatever is
// buffered and signals completion. Safe to call on a disabled client (it
// returns at once).
func (c *Client) Run(ctx context.Context) {
	if !c.enabled {
		return
	}
	defer close(c.done)

	t := time.NewTicker(flushInterval)
	defer t.Stop()

	batch := make([]hit, 0, batchMax)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		c.send(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Drain anything already queued, then a final flush, so
			// hits enqueued during shutdown aren't lost.
			for {
				select {
				case h := <-c.ch:
					batch = append(batch, h)
					if len(batch) >= batchMax {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case h := <-c.ch:
			batch = append(batch, h)
			if len(batch) >= batchMax {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

// Wait blocks until Run has flushed and returned (or returns immediately
// for a disabled client). Call after the HTTP server has shut down.
func (c *Client) Wait() { <-c.done }

func (c *Client) send(batch []hit) {
	body, err := json.Marshal(countBody{Hits: batch})
	if err != nil {
		log.Error("analytics marshal failed", "err", err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		log.Error("analytics request build failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	// GoatCounter matches the site by Host header; the app reaches the
	// container over the internal Docker network, so override it.
	req.Host = c.site

	resp, err := c.http.Do(req)
	if err != nil {
		log.Warn("analytics post failed", "err", err, "n", len(batch))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Warn("analytics post rejected", "status", resp.StatusCode, "n", len(batch))
	}
}
