// perftest hits every route on the local server and prints a latency report.
//
// Usage:
//
//	go run ./cmd/perftest              # 10 runs per route, print report
//	go run ./cmd/perftest -n 25        # 25 runs per route
//	go run ./cmd/perftest -r           # save a JSON report to perftest/
//	go run ./cmd/perftest -base http://staging:8080
//
// Each URL reports aggregate timings over all requests, including response bodies.
//
// Routes are auto-discovered: transit route IDs from /api/transit/routes,
// minutes IDs from page links.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const recordDir = "perftest"

type route struct {
	Group string `json:"group"`
	Path  string `json:"path"`
}

type result struct {
	route
	stats
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"`
}

// record is the JSON-serializable format for saved runs.
type record struct {
	Timestamp string   `json:"timestamp"`
	Base      string   `json:"base"`
	Runs      int      `json:"runs"`
	Routes    []result `json:"routes"`
}

func main() {
	base := flag.String("base", "http://localhost:8080", "server base URL")
	n := flag.Int("n", 10, "requests per route (minimum 1)")
	save := flag.Bool("r", false, "record results to "+recordDir+"/")
	flag.Parse()
	if *n < 1 {
		fmt.Fprintln(os.Stderr, "-n must be at least 1")
		os.Exit(1)
	}

	// Health check
	resp, err := http.Get(*base + "/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "server not reachable at %s: %v\n", *base, err)
		os.Exit(1)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "health check failed: %s\n", resp.Status)
		os.Exit(1)
	}

	// Discover parameterized route values from running server.
	routeIDs := discoverRouteIDs(*base)
	minutesID := discoverMinutesID(*base)

	routes := buildRoutes(routeIDs, minutesID)

	fmt.Printf("perftest: %d routes × %d runs against %s\n", len(routes), *n, *base)

	// Run benchmarks grouped by section.
	results := make([]result, 0, len(routes))
	curGroup := ""
	for _, r := range routes {
		if r.Group != curGroup {
			curGroup = r.Group
			printGroupHeader(curGroup)
		}
		res := bench(*base, r, *n)
		results = append(results, res)
		printResult(res)
	}

	// Summary
	fmt.Println()
	printSummary(results)

	// Save record
	if *save {
		path, err := saveRecord(*base, *n, results)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n  record save failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n  Recorded to %s\n", path)
	}
}

func buildRoutes(routeIDs []string, minutesID string) []route {
	routes := []route{
		// --- App pages ---
		{"Pages", "/"},
		{"Pages", "/budget"},
		{"Pages", "/councillors"},
		{"Pages", "/minutes"},
		{"Pages", "/about"},
		{"Pages", "/health"},
		{"Pages", "/version"},
	}

	// Minutes detail
	if minutesID != "" {
		routes = append(routes, route{"Pages", "/minutes/" + minutesID})
	}

	// Searches
	routes = append(routes, []route{
		{"Search", "/minutes?q=transit"},
		{"Search", "/minutes?q=budget"},
		{"Search", "/minutes?q=housing+affordable"},
		{"Search", "/minutes?term=2022"},
		{"Search", "/minutes?votes=1"},
		{"Search", "/minutes?defeated=1"},
		{"Search", "/councillors?term=2018"},
	}...)

	// Transit pages
	routes = append(routes, []route{
		{"Transit Pages", "/transit/"},
		{"Transit Pages", "/transit/kiosk"},
		{"Transit Pages", "/transit/metrics"},
		{"Transit Pages", "/transit/routes"},
		{"Transit Pages", "/transit/method"},
	}...)
	for _, id := range routeIDs {
		routes = append(routes, route{"Transit Pages", "/transit/route/" + id})
	}
	for _, id := range routeIDs {
		routes = append(routes,
			route{"Transit Partials", "/transit/route/" + id + "?partial=schedule"},
			route{"Transit Partials", "/transit/route/" + id + "?partial=schedule-body"},
		)
	}

	// Transit API
	routes = append(routes, []route{
		{"Transit API", "/api/transit/vehicles"},
		{"Transit API", "/api/transit/vehicles.json"},
		{"Transit API", "/api/transit/stats"},
		{"Transit API", "/api/transit/stats?range=percentiles"},
		{"Transit API", "/api/transit/stats?range=week"},
		{"Transit API", "/api/transit/stops"},
		{"Transit API", "/api/transit/routes"},
		{"Transit API", "/api/transit/timepoints"},
		{"Transit API", "/api/transit/stops/nearby?lat=48.38&lon=-89.25"},
		{"Transit API", "/api/transit/stops/analytics"},
	}...)

	return routes
}

func bench(base string, r route, n int) result {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res := result{route: r}
	times := make([]time.Duration, 0, n)

	for i := 0; i < n; i++ {
		start := time.Now()
		resp, err := client.Get(base + r.Path)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.Code = resp.StatusCode
		_, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		elapsed := time.Since(start)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			res.Error = "HTTP " + resp.Status
			return res
		}
		if readErr != nil {
			res.Error = "read response body: " + readErr.Error()
			return res
		}
		if closeErr != nil {
			res.Error = "close response body: " + closeErr.Error()
			return res
		}
		times = append(times, elapsed)
	}
	res.stats = calcStats(times)
	return res
}

// --- Output ---

func printGroupHeader(name string) {
	fmt.Printf("\n  %-65s %7s %7s %7s %7s %7s %4s\n",
		name, "Min", "Avg", "Med", "P95", "Max", "Code")
	fmt.Printf("  %-65s %7s %7s %7s %7s %7s %4s\n",
		strings.Repeat("─", 65), "─────", "─────", "─────", "─────", "─────", "────")
}

func printResult(r result) {
	if r.Error != "" {
		fmt.Printf("  %-65s  ERROR: %s\n", r.Path, r.Error)
		return
	}
	fmt.Printf("  %-65s %5dms %5dms %5dms %5dms %5dms %4d\n",
		r.Path, r.Min, r.Avg, r.Med, r.P95, r.Max, r.Code)
}

type stats struct {
	Min int64 `json:"min_ms"`
	Avg int64 `json:"avg_ms"`
	Med int64 `json:"med_ms"`
	P95 int64 `json:"p95_ms"`
	Max int64 `json:"max_ms"`
}

func calcStats(times []time.Duration) stats {
	if len(times) == 0 {
		return stats{}
	}
	ms := make([]int64, len(times))
	var sum int64
	for i, t := range times {
		ms[i] = t.Milliseconds()
		sum += ms[i]
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })

	n := len(ms)
	med := ms[n/2]
	if n%2 == 0 {
		med = (ms[n/2-1] + ms[n/2]) / 2
	}
	p95idx := int(math.Ceil(float64(n)*0.95)) - 1
	if p95idx >= n {
		p95idx = n - 1
	}

	return stats{
		Min: ms[0],
		Avg: sum / int64(n),
		Med: med,
		P95: ms[p95idx],
		Max: ms[n-1],
	}
}

func printSummary(results []result) {
	var slow []result
	var errs []result
	for _, r := range results {
		if r.Error != "" {
			errs = append(errs, r)
		} else if r.Avg > 50 {
			slow = append(slow, r)
		}
	}

	if len(errs) > 0 {
		fmt.Printf("  ERRORS (%d):\n", len(errs))
		for _, r := range errs {
			fmt.Printf("    %s — %s\n", r.Path, r.Error)
		}
		fmt.Println()
	}

	if len(slow) > 0 {
		fmt.Printf("  SLOW (>50ms avg):\n")
		for _, r := range slow {
			fmt.Printf("    %s — avg %dms, p95 %dms\n", r.Path, r.Avg, r.P95)
		}
	} else if len(errs) == 0 {
		fmt.Println("  All routes at or below 50ms avg.")
	}
}

// --- Recording ---

func saveRecord(base string, n int, results []result) (string, error) {
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		return "", err
	}

	now := time.Now()
	rec := record{
		Timestamp: now.Format(time.RFC3339),
		Base:      base,
		Runs:      n,
		Routes:    results,
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}

	filename := now.Format("2006-01-02T15-04-05") + ".json"
	path := filepath.Join(recordDir, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// --- Discovery helpers: fetch real IDs from the running server ---

func discoverRouteIDs(base string) []string {
	body := fetchBody(base + "/api/transit/routes")
	if body == "" {
		return nil
	}
	var routes []struct {
		RouteID string `json:"route_id"`
	}
	if err := json.Unmarshal([]byte(body), &routes); err != nil {
		return nil
	}
	// Sample up to 3
	ids := make([]string, 0, 3)
	for i, r := range routes {
		if i >= 3 {
			break
		}
		ids = append(ids, r.RouteID)
	}
	return ids
}

func discoverMinutesID(base string) string {
	body := fetchBody(base + "/minutes")
	if body == "" {
		return ""
	}
	// Find first /minutes/NNN link
	for _, part := range strings.Split(body, `href="/minutes/`) {
		idx := strings.IndexByte(part, '"')
		if idx > 0 && idx < 10 {
			id := part[:idx]
			if isDigits(id) {
				return id
			}
		}
	}
	return ""
}

func fetchBody(url string) string {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(b)
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
