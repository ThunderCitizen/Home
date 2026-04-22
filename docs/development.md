# Development

## Dev Container (Recommended)

The easiest way to get started is with a dev container. It provides Go, Node, Postgres, and all tooling pre-configured.

1. Open the repo in VS Code or Zed
2. Choose "Reopen in Container" (VS Code) or use the dev container panel (Zed)
3. Wait for the container to build and `make deps && npm install` to complete
4. Run `make dev` — server starts on http://localhost:8080

The dev container includes:
- Go 1.25 with templ, air, and migrate
- Node.js LTS (for Sass)
- PostgreSQL 16 (auto-started, no setup needed)
- VS Code extensions: Go, Templ, Sass, SQLTools

Environment variables (`DATABASE_URL`, `PORT`, `ENVIRONMENT`) are pre-configured.

## Local Setup

### Prerequisites

- Go 1.24+
- Node.js (for Sass/Pico CSS)
- PostgreSQL (or use Docker)

### Install Dependencies

```bash
make deps    # Install Go tools: templ, air, migrate
npm install  # Install Sass and Pico CSS
```

## Running Locally

```bash
make dev       # Hot reload (watches .go, .templ, .scss)
make dev-once  # Run without hot reload
```

Requires Postgres running locally or via `docker compose up db`.

## Individual Commands

```bash
make generate  # Generate templ files
make css       # Build CSS (Sass)
make css-watch # Watch CSS for changes
make lint      # Run go vet + ESLint
make lint-js   # ESLint only
```

## Operator Tools

`make all` builds every helper binary into `bin/` (gitignored).

### Refreshing source data

```bash
./bin/fetcher              # interactive menu
./bin/fetcher budget       # Ontario FIR data
./bin/fetcher gtfs         # Thunder Bay GTFS schedule
./bin/fetcher votes        # eSCRIBE council meetings
./bin/fetcher wards        # Open North ward boundaries
./bin/fetcher chunks       # rebuild transit chunks for a date range
```

`fetcher` is interactive only — every subcommand previews URLs and prompts `[y/N]` before downloading. See [cmd/fetcher/README.md](../cmd/fetcher/README.md) for the programmatic API.

### Shipping curated data to production

Curated state (councillors, budget ledger, council votes, wards) ships as a **signed muni bundle** — a set of TSVs plus `BOD.tsv` (bill-of-datasets), zipped and uploaded to DO Spaces. The server downloads, verifies, and applies it on boot, throttled by `muni_fetch_state.last_checked_at` (24h).

```bash
make muni-publish          # extract + sign + zip + upload in one step (= muni release)
```

Or drive the stages individually:

```bash
./bin/muni extract -out data/muni    # dev DB → TSVs + BOD.tsv
./bin/muni sign data/muni            # writes manifest.sig (autodetects your ~/.ssh key)
./bin/muni publish -dir data/muni    # zip + upload to DO Spaces
```

`./bin/muni publish -dry-run` (or `./bin/muni release -dry-run`) builds the bundle without uploading. Apply path: `internal/muni/apply.go` — skips datasets whose SHA-256 already appears in `data_patch_log`, errors on checksum drift.

### Other binaries

`summarize` (LLM motion classifier), `auditbudget` (sub-ledger balance check), `buildshapes` (route shapes from GTFS), `gentstypes` (TS interfaces from Go API structs), `perftest` (latency report), `seedtransit` (synthetic transit chunks for dev).

## What `make dev` Does

Air handles hot reload and runs these pre-build commands:

1. `gofmt -w ./internal ./cmd` - Format Go code
2. `templ generate` - Compile `.templ` to Go
3. `npm run css` - Compile SCSS to CSS

## Testing

### Unit Tests

```bash
go test ./...                      # Run all tests
go test ./templates/components/    # Run component tests only
```

Component tests (`templates/components/components_test.go`) verify Templ components render correctly.

### Visual Testing

Visual testing uses Playwright MCP for browser automation.

**Setup** (one-time):
1. Create `~/.claude/mcp.json`:
   ```json
   {
     "mcpServers": {
       "playwright": {
         "command": "npx",
         "args": ["@playwright/mcp@latest"]
       }
     }
   }
   ```
2. Restart Claude Code

**Usage**: Run `make dev`, then use Playwright tools to navigate pages and capture screenshots.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/thundercitizen?sslmode=disable` | Postgres connection |
| `PORT` | `8080` | Server port |
| `ENVIRONMENT` | `development` | Environment name |
