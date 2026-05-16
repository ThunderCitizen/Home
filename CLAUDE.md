# CLAUDE.md

## Commands

```bash
go test ./...             # All tests
make dev                  # Run with hot reload (auto-applies migrations + data patches on boot)
make all                  # Build every helper binary into bin/ (gitignored)
./scripts/backup.sh --dev     # Gzipped pg_dump via docker exec → ./backups/
templ generate            # Regenerate templ → Go
npm run css               # Rebuild SCSS → CSS
npm run build:js          # Rebuild TS → JS (leaflet shim)
./bin/fetcher             # Interactive: refresh source data (budget/gtfs/votes/wards)
make muni-extract         # Dev DB → TSV bundle in data/muni (applied on server boot)
make muni-publish         # data/muni → extract + sign + zip + upload to DO Spaces (= muni release)
go run ./cmd/perftest     # Latency report (server must be running)
go run ./cmd/perftest -r  # Record + delta vs last run (saves to perftest/)
```

## Key Patterns

- **Route → Handler → ViewModel → Template** (all pages)
- **pgx/v5** plain SQL, no ORM
- **templ** templates compile to Go — do not edit `*_templ.go`
- **Pico CSS** with Sass — do not edit `static/css/style.css`. `style.scss` is a coordinator that `@use`s partials: edit the appropriate one (`_tokens.scss`, `_mixins.scss`, `_placeholders.scss`, `_budget.scss`, `_transit.scss`, `_council.scss`)
- **Static source → signed muni bundle** for curated data (councillors, budget, council votes, wards). Fetchers in `cmd/fetcher` write `static/*.json` → `./bin/muni release` runs extract → sign → zip → upload to DO Spaces in one command (or use the underlying `extract`, `sign`, `publish` subcommands individually). On boot the server downloads the signed bundle, verifies the signature, and applies any new datasets via `internal/muni/apply.go` — tracked per-dataset in `data_patch_log` (checksum + signer), throttled by `muni_fetch_state.last_checked_at` (24h). No manual seed step.
- **Append-only `transit_*` event tables** for GTFS-RT data (recorder writes, everything else reads via SQL)
- **Standalone Go scripts** live in `cmd/` (e.g. `cmd/buildshapes`, `cmd/gentstypes`), not `scripts/`

## Shared Map Component

Both Transit and Council pages use Leaflet maps via a shared templ component in `templates/components/map.templ`.

### `LeafletMap(MapProps)`
Renders: Leaflet CDN, `.map-wrap` container, map div, children slot, page scripts. Shared behavior (scroll-zoom-on-click, zoom control positioning to bottom-left, `.map-active` focus ring) is handled by an embedded script that finds the Leaflet instance after page JS creates it. The component renders no header — every consumer puts its own `.terminal-map-header` above the map for visual parity.

```go
type MapProps struct {
    ID        string   // "transit-map", "ward-map"
    AriaLabel string
    Class     string   // extra CSS class ("transit-map-wrap", "ward-map-wrap")
    Scripts   []string // JS loaded after Leaflet
}
```

- **Header is page-owned** — render `<div class="terminal-map-header">` above `@LeafletMap` and put the layer toggles inside via `@MapLayerBar(groups)`. Page-scoped grid lives in `_transit.scss` (`.route-panel > .terminal-map-header`, `.terminal-board-page > .terminal-map-header`) and `style.scss` (`.ward-panel > .terminal-map-header`).
- **`MapLayerBar([]MapLayerGroup)`** — renders `<button data-layer="key">` toggles. Page JS reads `.active` class for initial state and wires click handlers.
- **Page JS owns `L.map()` init** — each page configures its own Leaflet options. The shared component doesn't call `L.map()`.

### Consumer patterns

**Transit** (`transit_live.templ` + `web/transit/transit-map.ts`): Custom `.terminal-map-header` with status / layers / features. Children of `@LeafletMap` = trip-planner overlay (slide-in side panel).

**Ward** (`councillors.templ` + `static/councillors/ward-map.js`): `.terminal-map-header` with title / subtitle / Wards layer toggle. Children = ward-info side overlay (reuses `.trip-planner-overlay` chrome) — slides in on hover/click with name + councillor, dismissed via Close.

## Color Theming

Terminal aesthetic — Solarized cream (light) + green phosphor (dark). Theme colors via CSS custom properties only (never hardcode hex); `color-mix()` for tints.

**Quick rules:**
- `--accent` → interactive (links/buttons/focus). `--heading` → headings/terminal labels. Never reuse one for the other.
- Tokens: spacing `--space-1..8`, type `--text-2xs..2xl`, weight `--weight-*`, radius `--radius-*` (in `_tokens.scss`). Snap to nearest, don't invent.
- Mono only — `--font-mono` is the single typeface; `--font-prose` is an alias kept for legacy callers.
- Pills/badges via `%badge-base` + size (`%badge-info` static / `%badge-action` interactive) + symmetric `badge-light-hue` / `badge-dark-hue` mixins. Mirror in both theme aggregators or it'll be themeless on one side.
- Cards: `%card-accent` (list rows) vs `%card-base` (standalone). Don't use either on table cells.

Full palette tables, typography stack, accessing colors from JS/SCSS, phosphor-pill recipe, header pattern matrix, what-not-to-tokenize → [docs/design-system.md](docs/design-system.md).

## Transit Page UI

- **Tab order**: Live, Kiosk, Metrics, Routes, Method
- **Kiosk tab** (route `/transit/kiosk`; `/transit/terminals` 301-redirects) shows real-time departures from four canonical terminals (Waterfront, City Hall, Confederation College, Lakehead University). Header has selectable terminal tabs + Kiosk mode toggle. Client polls `/api/transit/stop/{id}/predictions` every 15s (exponential backoff on error, pauses when tab hidden). Fullscreen/kiosk mode targets TV displays — locks to a fixed **3×3 grid** (`.terminal-card-grid` in `body.terminal-fullscreen`), no pagination/rotation: every realistic terminal has ≤9 active routes so all groups fit on one page. Canonical terminals hardcoded in `handler.go:canonicalTerminals` (4 stop IDs) — not data-driven, update manually if GTFS stop IDs change.
- **Terminal card** (`renderCard` in `static/transit/terminal-board.js`, CSS in `static/css/_transit.scss` `.terminal-card*`): extends `%card-base` for chrome (surface, border, radius, shadow, padding) and adds a 6px left-border that status variants (`-cancelled`, `-late`, `-early`, `-ontime`, `-scheduled`) recolor to encode arrival state. Layout is a two-row grid (header / body). Header is `pill + headsign + status` (3-col grid `auto minmax(0,1fr) auto` — headsign always ellipsizes, status floats right). Body is a 66/33 split: hero on the left (clock-time meta row, then big minutes value, then optional `(HH:MM sched)` line for late trips, then optional cancelled banner), Then/Later cells stacked vertically on the right with a left divider separating them from the hero. Hero is `white-space: nowrap` with both axes bounded so it never wraps or overflows. Pico's default `article > header` chrome (border-bottom, sectioning bg, negative margins) is explicitly overridden on `.terminal-card-head`.
- **Web vs kiosk sizing for the card are independent on purpose.** Web view uses `clamp(rem, vw, rem)` tuned for arm's-length screen reading — keep it small and practical so info isn't lost behind ellipses on phones. Kiosk uses `body.terminal-fullscreen .terminal-card { container-type: size }` plus a `@container tcard` block at the bottom of `_transit.scss` that rewrites every `--tc-*-fs` token as `min(<cqi>, <cqh>)` so each glyph fills whichever axis runs out first — the values are tuned to fill, not to mirror web. Don't reintroduce a "scaled copy of the web card" coupling; web changes shouldn't ripple into kiosk and vice versa.
- **Metrics tab** has 6 KPI cards in a 3×2 grid, a trend chart (click card to switch KPI), and a route comparison bar chart
- **KPI card convention**: main value in `.kpi-value`, three sub-slots showing Morning/Midday/Evening breakdown. Server-rendered via `KPIFromChunks(vm.Chunks, metric, band)` in `view_helpers.go`
- **6 metrics** (ordered simplest→hardest, matching Method tab): OTP, Cancellation Rate, Cancel Notice, Stop Wait, EWT, Headway CV
- **Time bands** — three 6-hour windows defined once in `metrics.go:Bands`: **Morning** (6–12), **Midday** (12–18), **Evening** (18–24). Hours outside 6–24 are excluded — Thunder Bay Transit doesn't run before 6am.
- **Chunk model** — the metric unit is one **chunk**: 1 route × 1 day × 1 band, persisted as one row in `transit.route_band_chunk` (migration `000003`). Each chunk stores raw counts and SUM-stable headway sums (`headway_sum_sec`, `headway_sum_sec_sq`, `sched_headway_sec`), so any aggregation across routes/days/bands is exact arithmetic — never an average of percentages.
- **Recipes (write path)** — `BuildChunksForDate` in `internal/transit/chunk.go` runs five small per-metric SQL queries from `internal/transit/recipes/` (`service_kind`, `otp`, `cancel`, `baseline`, `headway`), one per chunk, then upserts the assembled `chunk.Chunk`. Each recipe is its own file with a SQL constant and a Go function so the formulas can be audited in isolation. The chunk math itself (`Cv`, `EWTSec`, `WaitMin`, `ComputeSystem`, `ComputeRoutes`) lives in `internal/transit/chunk/` with textbook unit tests.
- **Read path** — `Service.Chunks(ctx, from, to)` returns `[]chunk.ChunkView` from `ChunkCache` (`chunk_cache.go`), which lazy-loads from `transit.route_band_chunk` and caches forever per (route, date, band) — `today` is the only key allowed to refresh, everything else is immutable history.
- **Aggregation** — `KPIFromChunks` and `RouteRowKPIFromChunks` in `view_helpers.go` route through `chunk.KPI` (`internal/transit/chunk/rollup.go`). For OTP, cancellation rate, cancel notice, and wait it sums raw counts across the slice and divides once. **Cv and EWT are different**: they pool headway sums per route, compute the metric per route, then take an unweighted arithmetic mean across routes (each route weighted equally regardless of trip volume) — necessary because Cv/EWT are nonlinear in the underlying sums. Empty band string (`""`) pools all three bands. The mirror frontend port is `static/transit/chunks.js` (`window.transitChunks.aggregate`) — same formulas and split, used by `trends-chart.js` for the route comparison chart.
- **No KPI endpoint** — KPIs are server-rendered into the page via `KPIFromChunks` and the chunks themselves are embedded via `@templ.JSONScript("transit-chunks", vm.Chunks)` for client-side aggregation. There is no `/api/transit/kpis` or `/api/transit/chunks` — the chunks data only travels with the page.
- **Auto-rollup** — `internal/transit/chunk_rollup.go` runs in a goroutine wired in `cmd/server/main.go` next to the recorder. On boot it backfills any date in the last 60 days where events exist but chunks don't; then every 10 min it rebuilds today's chunks. Idempotent upserts. Without this, `route_band_chunk` stays empty and every KPI renders blank — prod hit exactly this before the rollup existed; dev masked it because `seedtransit` pre-fills synthetic chunks.
- **Manual rebuilds** — `./bin/fetcher chunks` interactively rebuilds chunks for a date range (use after changing a recipe or to fill deeper than the auto-backfill window). `./bin/seedtransit` writes synthetic chunks for the dev DB when GTFS hasn't been loaded.
- **Cache layer** — non-chunk cached data products live in `RepoCache` (`repo_cache.go`) as `CacheSlot[T]` / `CacheMap[K,V]` fields, with double-checked-locking lazy-load primitives in `cache.go`. The `live` slot is the only one with a TTL (30s via `NewCacheSlotTTL`); everything else caches forever. Chunks live in their own `ChunkCache`, not in `RepoCache`.
- **Browser cache-control** — Five named strategies in `internal/cache/cache.go`: `Live` (`no-cache`, SSE/realtime feeds), `Short` (30s, predictions/distance/nearby stops), `Page` (5 min, HTML pages and search), `Reference` (1h immutable, GTFS-derived bulk data like routes/stops), `Static` (1 week immutable, `/static/*`). Every handler that sets `Cache-Control` references one of these constants — grep `cache.Live`, `cache.Short`, etc. In non-production environments, `middleware.NoCacheInDev` (wired in `cmd/server/main.go`) wraps the response writer and overwrites every Cache-Control to `no-store` right before the first byte ships, so dev never sees stale work regardless of which strategy a handler picked. In production it's a no-op.
- **pgx query mode** — `DefaultQueryExecMode = QueryExecModeCacheDescribe` in `database/db.go`. Cache the parameter type descriptions but re-plan every query; the default `CacheStatement` switches to a Postgres generic plan after 5 executions and picks a pathological join order for the recipe queries.
- **Stop visit detection** uses line-segment interpolation between consecutive GPS positions (`segmentDistToPoint` in `vehicle_tracker.go`), not just point proximity. Catches stops the bus passed between 15-second GPS readings
- **Route finder** is an accordion overlay pinned to the top-right of the map (`trip-planner-overlay`). Collapsed = tab, expanded = form + results panel with fixed 380px height
- **Form layout** uses `display: table` inside the overlay body — labels as tight left cells, inputs fill the right
- **Cancellation badges** on route pills split into two: red "X upcoming" and gray "Y earlier". Stat bar matches the same split using `upcomingCancelledTrips` / `pastCancelledTrips` (both count trips, not incidents)
- **Stop predictions API** returns `{ predictions: [...], updated_at }` — the `updated_at` is the GTFS-RT feed timestamp, shown as "Updated Xs ago" in stop popups
- **Stop hover** on map enlarges the marker (+3 radius) and shows a tooltip with the stop name
- **Skeleton loading** — route grid shows pulsing pill shapes, live stats show skeleton text blocks (`.skeleton` / `.skeleton-text` / `.skeleton-pill` classes with `skeleton-pulse` animation)
- **Map container** uses shared `LeafletMap` with `Class: "transit-map-wrap"` for terminal theming. Transit's custom `terminal-map-header` sits above it with title, layer bar, and Features controls.
- **Zoom buttons** — shared component positions them bottom-left. Pico CSS overrides ensure `+` has rounded top, `-` has rounded bottom
- **Selector bars (segmented control)** — `%segmented-shell` (in `_placeholders.scss`) is the **single source of truth** for any tab/button strip: outer 1px `--term-border` + radius, inner `padding: 3px`, `gap: 0`, and a `> * + *` rule that paints 1px dividers between flush siblings via **inset `box-shadow`** (not `border` — children routinely reset `border: 0` to kill the default button outline and an equal-specificity `border-left` loses on source order). `.selector-bar-btns` and `.terminal-tab-group` both `@extend` it; any new strip control must do the same — don't reinvent the chrome and don't redefine the dividers locally (visual consistency across the whole app depends on this). Don't reintroduce `gap`/`margin` — the placeholder's adjacent-sibling rule needs flush children. When the strip switches to `display: grid` for a 2-col wrap (mobile Terminals), keep `gap: 0` and re-paint the dividers per axis with box-shadow: `:nth-child(2n)` gets a left edge, `:nth-child(n+3)` a top edge, the bottom-right cell both. Adjacent shells line up vertically by giving each `.selector-bar-label` the same `min-width` + `text-align: end`.
- **`.terminal-map-header` is shared chrome only** — base class sets bg/border/scanlines, but layout is page-scoped: `.route-panel > .terminal-map-header` owns the live-map grid (status / layers / features), `.terminal-board-page > .terminal-map-header` owns the terminals grid (title / tabs / kiosk). Don't add `> #x` rules to the base — they leak into the other page.
- **One-way info bar** — clicks on the *map* (route line, bus marker, stop) push state up into the top info bar via `lockInfoBar`. Clicks on the *route cards below the map* call `selectRoute(route, "card")` and intentionally skip the info-bar update. Why: card clicks are list filters, not map interactions; pushing them up made the info bar feel like a duplicate selection display. Keep the source param when adding new entry points.
- **Find Route is a `<button>`** that toggles `.is-open` on `.trip-planner-overlay` via `setTripPlannerOpen` in `transit-map.ts`. The Close affordance inside the overlay is also a `<button>`. Earlier revision used a hidden checkbox + `<label>` + `:has(.trip-toggle:checked)` to avoid JS, but `<label>` rendered with subtly different vertical alignment than its sibling `<button>` (Locate) in the Features bar, so the toggle moved to JS-driven class state for visual parity.
- **Late-trip "scheduled" line** is a sibling under `.terminal-card-hero-meta`, not appended to the time string. Renders as `(HH:MM sched)` only when status kind is `late` AND scheduled differs from predicted. Old "(was HH:MM)" suffix ellipsized off-screen on phones; the new sibling wraps onto its own line.

## Councillors Page UI

Members are a card grid (`councillorCard(c, kind)` → `.councillor-grid`), **not** an accordion — bio + vote stats always visible.

**Quick rules:**
- `kind` (`"mayor"`/`"atlarge"`/`"ward"`) comes from the caller — the view model already groups members; never infer it from `c.Position`.
- Grid is column-capped (3 / 2 / 1 at `lg` / `sm`), not `auto-fill minmax()` — max 3 wide is intentional.
- One `.councillor-card-role` pill per card: Mayor gold, At-Large teal (non-geographic on purpose), Ward per-ward color.
- Ward pill hexes must stay in sync across `badge-light/dark-overrides-council` (`_council.scss`) **and** `WARD_COLORS` in `ward-map.js`.

Grid/breakpoints, role-pill + badge slots, ward-color sync, vote-summary nowrap, sticky-header resync → [docs/council.md](docs/council.md#councillors-page-ui).

## Responsive Patterns

- Sticky table headers (CSS-only inside flow; JS-synced clone inside `overflow-x: auto`).
- Multi-group toolbar wrap → CSS grid with named areas + `display: contents` on group wrappers (so labels align across rows). Never `flex-wrap` a labeled toolbar.

Recipes, gotchas, examples → [docs/responsive-patterns.md](docs/responsive-patterns.md).

## Docs

- [docs/architecture.md](docs/architecture.md) - Stack, request flow, data provenance
- [docs/development.md](docs/development.md) - Local setup and commands
- [docs/database.md](docs/database.md) - Schema, PostGIS, indexes, connection pooling
- [docs/docker.md](docs/docker.md) - Docker Compose services and commands
- [docs/design-system.md](docs/design-system.md) - Color tokens, typography, cards, phosphor pills
- [docs/responsive-patterns.md](docs/responsive-patterns.md) - Sticky headers, toolbar wrap grid
- [docs/transit.md](docs/transit.md) - GTFS-RT feeds, recorder, trip planner (RAPTOR), PostGIS
- [docs/transit-metrics.md](docs/transit-metrics.md) - Performance KPIs, methodology, incident detection
- [docs/council.md](docs/council.md) - Council minutes scraping, vote parsing
- [docs/summarize-motions.md](docs/summarize-motions.md) - LLM motion classification runbook
- [docs/data-visualization.md](docs/data-visualization.md) - Chart selection and principles
- [docs/accessibility.md](docs/accessibility.md) - WCAG 2.2 AA targets and compliance notes
- [cmd/fetcher/README.md](cmd/fetcher/README.md) - Manual fetcher CLI and programmatic API
- [cmd/seedtransit/README.md](cmd/seedtransit/README.md) - Synthetic transit chunks for dev
- [DEPLOY.md](DEPLOY.md) - Dev + prod deployment (docker-compose on a Debian box + Caddy)
