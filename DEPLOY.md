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
apt-get update -qq && apt-get install -y -qq git python3
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

`deploy.sh` uses two HTTP slots (`app-blue` and `app-green`). The serving app stays up while `transitprepare -action prepare` builds partitioned replacements online and the new candidate warms. The script checks Docker health and the home, Daily, Monthly, and Method pages before gracefully reloading Caddy onto that candidate.

The candidate's background writers remain disabled. After the HTTP switch succeeds, the script stops the previous app, runs `transitprepare -action cutover`, then atomically selects the new writer slot in `.deploy/active`. Production HTTP containers skip startup migrations; the preparation CLI owns migration work explicitly. The old container/image is retained, not removed during rollout.

If candidate health or routing fails, the previous writer stays running. If the final schema cutover fails after stopping that writer, the candidate continues serving read-only and `.deploy/transition` records the unfinished step. Rerun `./scripts/deploy.sh --no-git` to resume with the **same checked candidate image**, rather than pulling a different build. Do not restart the old binary after a schema cutover attempt: its writer SQL may no longer match the schema. Once the schema transition is complete, roll back application changes by deploying a compatible image through the same script.

Caddy's selected configuration persists in its `/config` volume, so a restart keeps the active upstream. **The first upgrade from the original single-app stack requires one brief Caddy recreation** to adopt that startup behavior; it happens only after a healthy candidate and its routing configuration exist. Later deployments use graceful Caddy reloads without a proxy restart.

The deployer renders the current Compose Caddy configuration with Python 3, validates it, and reloads it with the selected upstream. Site configuration edits therefore take effect on the next deployment as well.

The host `.deploy/` directory is deployment state, not disposable build output. Preserve it across deployments and backups. A host `flock` prevents concurrent deployers. Use this script for production rollouts; a bare `docker compose up` does not perform preparation, health checks, or writer activation.

For the **first upgrade from the old deploy script**, wait for the matching image build to publish, then explicitly update the script before executing it:

```bash
git pull --ff-only
./scripts/deploy.sh --no-git
```

The old script cannot safely upgrade itself mid-execution: it continues its original app-restart procedure after pulling the new Compose file. Once this version is installed, normal `./scripts/deploy.sh` calls re-execute the updated script after pulling. If `TC_IMAGE_TAG` is pinned, update it to the published build containing these changes first.

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
chown 1001:1001 ./secrets/goatcounter_token
chmod 600 ./secrets/goatcounter_token
./scripts/deploy.sh --no-git                      # picks up the token
```

View the dashboard (nothing is exposed publicly):

```bash
ssh -L 8081:localhost:8081 root@<box>
# one-time on your laptop, add to /etc/hosts:  127.0.0.1 analytics.local
# then open http://analytics.local:8081
```

The SQLite DB lives in the `goatcounter_data` volume — wire it into your off-host backup flow alongside the Postgres dump if you want retained history.

Behavior notes (verified): the middleware sends every GET HTML pageview; GoatCounter dedupes same-session reloads (so a kiosk/tab left open does **not** inflate counts) and auto-classifies bots into a separate, excluded-but-viewable category (no bot pre-filter in the app on purpose — keeps "human vs bot" auditable). Pageviews persist from an in-memory buffer on a short interval, so counts lag live traffic by a few seconds.

### Transit monthly archive bucket

Production defaults to the `thundercitizen` bucket and
`tor1.digitaloceanspaces.com` endpoint. Supply the credentials below to enable
automatic archival; without usable credentials, raw data remains in the DB.
Place bucket-scoped access and secret keys in
`secrets/transit_archive_access_key` and `secrets/transit_archive_secret_key`
owned by UID/GID 1001 (the container's app user), with mode 0600:

```bash
chown 1001:1001 secrets/transit_archive_access_key secrets/transit_archive_secret_key
chmod 600 secrets/transit_archive_access_key secrets/transit_archive_secret_key
```

Compose exports these for the server and fails preparation if an existing key
file cannot be read. Credentials need object
read/write and ACL permissions: raw objects stay private, while only the
consolidated ZIP becomes public. Do not configure raw-object expiration.

For the `thundercitizen` bucket, use:

```dotenv
TRANSIT_ARCHIVE_BUCKET=thundercitizen
TRANSIT_ARCHIVE_ENDPOINT=tor1.digitaloceanspaces.com
```

Automatic uploads are grouped under `transit/raw/` (private archives) and
`transit/reports/archive/` (public ZIP releases). Uploads create these prefixes automatically.
Create a dedicated production Spaces key with **Limited access** to
`thundercitizen` and **Read/Write/Delete** permissions. These standard keys are
bucket-scoped, not folder-scoped: `transit/` organizes files but does not restrict
the key's access to other objects in the bucket. A separate bucket is the simple
option if that isolation is required. Limited keys cannot coexist with S3 bucket
policies; keep raw data private and publish only the ZIP object ACLs.
See [DigitalOcean's access-key documentation](https://docs.digitalocean.com/products/spaces/how-to/manage-access/).

The deploy script prepares monthly partitions while the existing app serves and
records data, then stops its writer for a short atomic schema switch. Back up
first and allow disk headroom for a replacement of the largest raw table and its
indexes, plus WAL and working space. GPS and alert originals are released after
each is converted; the other six tables share the final handoff. Do not manually run migration 30 on the live stack.
See the [incremental rehearsal measurements](docs/transit-incremental-rehearsal.md)
for capacity planning; the previous twice-total-data estimate was unmeasured.
The preparation preserves records; enabling the bucket starts automatic catch-up
and verified eviction of completed months. See
[the lifecycle and recovery runbook](docs/transit-archival.md).
