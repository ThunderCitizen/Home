# AGENTS.md

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
go run ./cmd/perftest -r  # Save latency report to perftest/
go run ./cmd/ogshot       # Regenerate social-share OG card → static/og/*.png (server must be running)
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

Transit and Council pages share `LeafletMap(MapProps)` in `templates/components/map.templ`. It renders Leaflet + container + a children slot but **never calls `L.map()`** and renders **no header** — each page owns its `.terminal-map-header` and inits Leaflet itself. `.terminal-map-header` is shared chrome only; its grid layout is page-scoped, so don't add `> #id` rules to the base class (they leak into the other pages).

MapProps fields, `MapLayerBar`, zoom-button positioning, per-page consumer patterns → [docs/transit.md](docs/transit.md#frontend--ui-patterns).

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

Tab order: **Live, Kiosk, Metrics, Routes, Method**.

- **Metrics = versioned chunk model.** One route × day × band in `transit.route_band_chunk`, storing additive counts and matched wait integrals. `metric_schedule.go` archives GTFS; `metric_passage.go` screens/reconstructs GPS; `metric_day.go` reads each source once per day; `recipes/window.go` calculates same-window waits/CV; `chunk.go` replaces the day atomically. OTP classifies individual reported departure timepoints. CV is computed within stop/direction/day/band windows; EWT compares actual and scheduled waits over identical intervals. Per-route duration-weighted EWT/CV are averaged equally across contributing routes. Missing and legacy-version values stay unavailable, never zero. Go/JS reducers must agree. Background rollups repair stale/missing days and finalize yesterday. Use `./bin/transitmetrics -from YYYY-MM-DD -to YYYY-MM-DD -gps` for archived recovery. No KPI endpoint; chunks ride the page via `@templ.JSONScript`. Full definitions, limitations and rebuilds → [docs/transit-metrics.md](docs/transit-metrics.md).
- **Server reads.** Metadata, stops, analytics, chunks, cancellation/route details, stats reports, and dashboard data query SQL on each request. `Service` does not cache report or reference results. Optimize the requested route/date range, aggregate in SQL, and use `EXISTS` for boolean facts. Recorder/feed state, vehicle streaming, and the planner's timetable remain separate. [Read architecture](docs/transit.md#server-reads) · [Query measurements and validation](docs/route-performance.md).
- **HTTP and database caching.** Browser cache-control uses five named strategies in `internal/cache/cache.go`: `Live`/`Short`/`Page`/`Reference`/`Static`. `NoCacheInDev` forces `no-store` in dev. pgx caches parameter descriptions with `QueryExecModeCacheDescribe`, not query results; see [docs/database.md](docs/database.md).
- **Kiosk** (`/transit/kiosk`; `/transit/terminals` 301s) — four hardcoded terminals (`handler.go:canonicalTerminals`), fixed **3×3 grid, no pagination**, 15s polling with backoff. Terminal-card layout + web/kiosk sizing independence → [docs/transit.md](docs/transit.md#kiosk-page-transitkiosk).
- **Stop visit detection** uses line-segment interpolation between GPS fixes (`segmentDistToPoint` in `vehicle_tracker.go`), not just point proximity — catches stops passed between 15s readings.
- **Selector bars / segmented controls** — always `@extend %segmented-shell` (`_placeholders.scss`); it's the single source of truth for strip chrome + box-shadow dividers. Don't reinvent it, redefine dividers locally, or add `gap`/`margin` (the adjacent-sibling rule needs flush children).
- **Other live-page UI gotchas** (one-way info bar, Find-Route `<button>`, route-finder overlay + form, cancellation badges, skeletons, stop hover) → [docs/transit.md](docs/transit.md#frontend--ui-patterns).

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

## Analytics

Server-side only — `internal/analytics` middleware (`Track`) reports GET pageviews for HTML routes (skips `/api/*`, `/static/*`, `/health`, `/version`, non-2xx) to a self-hosted GoatCounter over the internal Docker network. No client JS, no pixel, no cookies. Visitor IP + UA are passed so GoatCounter computes its own daily-salted session hash (uniques); we don't store them. **No bot pre-filter on purpose** — GoatCounter classifies bots itself and keeps them excluded-but-auditable; filtering here would only discard that evidence. Disabled (pass-through) unless `GOATCOUNTER_URL`/`GOATCOUNTER_TOKEN` are set, so dev is unaffected. The GoatCounter container is **not** in the Caddyfile and has no public port — dashboard is SSH-tunnel only (see DEPLOY.md "Analytics"). `/about`'s "no analytics pixels / no tracking cookies" claim stays accurate — server-side only, no pixel, no client script, no cookie.

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
