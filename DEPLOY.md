# Deploy

The stack is `docker-compose.prod.yml` — Postgres + app + Caddy — and runs on any Debian-family box with a public IP, a domain pointing at it, and ports 80/443 open. The scripts in `scripts/` do the actual work; this doc is the order to run them in.

## Dev

```bash
docker compose up          # db + app, app on :8080
```

Full dev workflow (dev container, `make dev`, hot reload, test commands) lives in [docs/development.md](docs/development.md).

## Prod

On a fresh Debian box as root:

```bash
# 1. DNS first — point the hostnames in docker-compose.prod.yml's Caddyfile
#    block at this box's public IP. TLS won't issue until DNS resolves.

# 2. Clone
mkdir -p /opt && cd /opt
apt-get update -qq && apt-get install -y -qq git
git clone --depth 1 https://github.com/thundercitizen/home.git
cd ThunderCitizen

# 3. Harden (ufw + fail2ban + unattended-upgrades; idempotent)
./scripts/harden.sh

# 4. Docker
curl -fsSL https://get.docker.com | sh

# 5. (Optional) override defaults
cat > .env <<'EOF'
ACME_EMAIL=you@example.com
TC_IMAGE_TAG=sha-3d84894       # pin an immutable build
EOF

# 6. Bring it up
./scripts/deploy.sh
```

`deploy.sh` is idempotent: cold-boots the full stack on first run, hot-bounces only the app container on subsequent runs. Use it for every rollout too — `./scripts/deploy.sh` after `git pull` is the full update path.

### Env overrides

| Var | Default |
|---|---|
| `ACME_EMAIL` | `ops@thundercitizen.ca` |
| `POSTGRES_USER` | `thundercitizen` |
| `POSTGRES_DB` | `thundercitizen` |
| `TC_IMAGE_TAG` | `latest` |

The canonical domain and redirect aliases are hardcoded in the inlined Caddyfile inside `docker-compose.prod.yml` — edit the compose file, not an env var. The Postgres password auto-generates on first boot into `./secrets/postgres_password`.

### Backups

```bash
./scripts/backup.sh            # → ./backups/thundercitizen-<UTC>.sql.gz
```

Daily cron at 04:00 UTC:

```cron
0 4 * * * cd /opt/ThunderCitizen && ./scripts/backup.sh >> /var/log/tc-backup.log 2>&1
```

Off-host shipping and retention pruning are intentionally not in the script — wire those to whatever you already use.

### Restore

```bash
docker compose -f docker-compose.prod.yml stop app
gunzip -c backups/thundercitizen-<timestamp>.sql.gz | \
  docker compose -f docker-compose.prod.yml exec -T db \
  psql -U thundercitizen -d thundercitizen
docker compose -f docker-compose.prod.yml start app
```

### Logs

```bash
docker compose -f docker-compose.prod.yml logs -f     # all services
tail -f caddy-logs/access.log                         # filtered access log
```

Database seeding is automatic — on boot the app downloads the signed muni bundle from `data.thundercitizen.ca`, verifies the signature, and applies any new datasets. Nothing to do by hand.

### Analytics

Self-hosted GoatCounter, fed server-side by the app's analytics middleware (no client JS, no cookies). It is **not** in the Caddyfile and has **no public port** — it binds to host loopback only and is reached over an SSH tunnel.

One-time setup on the box:

```bash
dc="docker compose -f docker-compose.prod.yml"
$dc up -d goatcounter

# Single site. The vhost MUST be analytics.local — the app sends API
# requests with that Host header and the dashboard is browsed via it.
# (GoatCounter rejects single-label vhosts like "localhost" — needs a dot.)
# This also creates user 1, which you log in with.
$dc exec goatcounter goatcounter db create site \
    -vhost analytics.local -user.email you@example.com -password '<pick-one>'

# API token for the app (user 1, count permission). The create command
# prints nothing on success — read the token back out with `db show`.
$dc exec goatcounter goatcounter db create apitoken -name app -user 1 -perm count
$dc exec goatcounter goatcounter db show apitoken -find 1   # copy the `token` value

printf '%s' '<token-from-show>' > ./secrets/goatcounter_token
chmod 600 ./secrets/goatcounter_token
$dc up -d app                                    # picks up the token
```

View the dashboard (nothing is exposed publicly):

```bash
ssh -L 8081:localhost:8081 root@<box>
# one-time on your laptop, add to /etc/hosts:  127.0.0.1 analytics.local
# then open http://analytics.local:8081
```

The SQLite DB lives in the `goatcounter_data` volume — wire it into your off-host backup flow alongside the Postgres dump if you want retained history.

Behavior notes (verified): the middleware sends every GET HTML pageview; GoatCounter dedupes same-session reloads (so a kiosk/tab left open does **not** inflate counts) and auto-classifies bots into a separate, excluded-but-viewable category (no bot pre-filter in the app on purpose — keeps "human vs bot" auditable). Pageviews persist from an in-memory buffer on a short interval, so counts lag live traffic by a few seconds.
