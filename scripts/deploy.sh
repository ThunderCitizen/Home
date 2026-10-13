#!/usr/bin/env bash
set -euo pipefail

# Health-checked two-slot rollout. The old HTTP app serves during online
# preparation and candidate warming. Resume interrupted cutovers by rerunning.
# Usage: ./scripts/deploy.sh [--no-git]
cd "$(dirname "$0")/.."
COMPOSE=(docker compose -f docker-compose.prod.yml --profile deploy)
GIT_PULL=1
for arg in "$@"; do
    case "$arg" in
        --no-git) GIT_PULL=0 ;;
        -h|--help) sed -n '4,6p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) printf 'unknown flag: %s\n' "$arg" >&2; exit 2 ;;
    esac
done
log() { printf '\n==> %s\n' "$*"; }
command -v python3 >/dev/null || { printf 'python3 is required to render the deployment configuration\n' >&2; exit 1; }
mkdir -p .deploy
chmod 755 .deploy
exec 9>.deploy/lock
flock -n 9 || { printf 'another deployment is running\n' >&2; exit 1; }
if [[ "$GIT_PULL" == 1 ]] && [[ -d .git ]]; then
    git pull --ff-only
    # Execute the newly pulled script, rather than continuing an old shell
    # body against a newer Compose file. Release and reacquire the host lock.
    exec bash scripts/deploy.sh --no-git 9>&-
fi
service_running() { [[ -n "$("${COMPOSE[@]}" ps -q --status running "$1")" ]]; }
write_state() { printf '%s %s %s\n' "$old" "$candidate" "$1" >.deploy/transition.next; mv .deploy/transition.next .deploy/transition; }
activate() { printf '%s\n' "$1" >.deploy/active.next; chmod 644 .deploy/active.next; mv .deploy/active.next .deploy/active; }
wait_healthy() {
    local service=$1 id status deadline=$((SECONDS + 180))
    while (( SECONDS < deadline )); do
        id=$("${COMPOSE[@]}" ps -q "$service")
        [[ -n "$id" ]] || return 1
        status=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id")
        case "$status" in healthy) return 0 ;; unhealthy|exited|dead) return 1 ;; esac
        sleep 2
    done
    return 1
}
reload_candidate() {
    # Preserve the currently serving route so a failed post-reload probe can
    # return traffic to it before touching the old writer.
    if [[ ! -f .deploy/previous.Caddyfile ]]; then
        "${COMPOSE[@]}" exec -T caddy sh -ec 'if [ -s /config/active.Caddyfile ]; then cat /config/active.Caddyfile; else cat /etc/caddy/Caddyfile; fi' >.deploy/previous.Caddyfile
    fi
    "${COMPOSE[@]}" cp .deploy/previous.Caddyfile caddy:/config/previous.Caddyfile
    # Render the desired Compose configuration, not an old container's mounted
    # file, so later site configuration edits are applied by graceful reload too.
    "${COMPOSE[@]}" config --format json | python3 -c 'import json,sys; sys.stdout.write(json.load(sys.stdin)["configs"]["caddyfile"]["content"])' >.deploy/base.Caddyfile
    sed -E "s/reverse_proxy app(-blue|-green)?:8080/reverse_proxy app-${candidate}:8080/" .deploy/base.Caddyfile >.deploy/candidate.Caddyfile
    grep -q "reverse_proxy app-${candidate}:8080" .deploy/candidate.Caddyfile || { printf 'could not locate managed Caddy upstream\n' >&2; return 1; }
    if ! grep -q '127.0.0.1:8081' .deploy/candidate.Caddyfile; then
        printf '\nhttp://127.0.0.1:8081 {\n    bind 127.0.0.1\n    reverse_proxy app-%s:8080\n}\n' "$candidate" >>.deploy/candidate.Caddyfile
    fi
    "${COMPOSE[@]}" cp .deploy/candidate.Caddyfile caddy:/config/candidate.Caddyfile
    "${COMPOSE[@]}" exec -T caddy caddy validate --config /config/candidate.Caddyfile --adapter caddyfile
    "${COMPOSE[@]}" exec -T caddy caddy reload --config /config/candidate.Caddyfile --adapter caddyfile
    "${COMPOSE[@]}" exec -T caddy mv /config/candidate.Caddyfile /config/active.Caddyfile
    # Adoption from the original single-app deployment requires one proxy
    # recreation so future restarts read the persisted selected upstream.
    local caddy_id command
    caddy_id=$("${COMPOSE[@]}" ps -q caddy)
    command=$(docker inspect --format '{{json .Config.Cmd}}' "$caddy_id")
    if [[ "$command" != *active.Caddyfile* ]]; then
        log "adopting persistent Caddy routing (one-time proxy restart)"
        "${COMPOSE[@]}" up -d --no-deps caddy
    fi
    # Exercise HTTP through the proxy after reload, before stopping old writers.
    if ! wait_proxy; then
        "${COMPOSE[@]}" exec -T caddy caddy reload --config /config/previous.Caddyfile --adapter caddyfile
        "${COMPOSE[@]}" exec -T caddy mv /config/previous.Caddyfile /config/active.Caddyfile
        printf 'proxy verification failed; restored previous route and retained old writer\n' >&2
        return 1
    fi
}

wait_proxy() {
    # The one-time recreation can return before Caddy starts listening.
    local attempt
    for attempt in {1..10}; do
        if "${COMPOSE[@]}" exec -T caddy wget -qO- http://127.0.0.1:8081/health | grep -qx OK; then
            return 0
        fi
        sleep 1
    done
    return 1
}

if [[ -s .deploy/transition ]]; then
    read -r old candidate phase <.deploy/transition
    [[ "$old" =~ ^(app|blue|green|none)$ && "$candidate" =~ ^(blue|green)$ && "$phase" =~ ^(prepared|routed|stopped)$ ]] || { printf 'invalid persisted deployment state\n' >&2; exit 1; }
    log "resuming $candidate deployment at $phase (no image replacement)"
else
    log "ensuring database and analytics are available"
    "${COMPOSE[@]}" up -d --wait db goatcounter
    old=none
    if [[ -s .deploy/active ]]; then
        read -r old <.deploy/active
        [[ "$old" =~ ^(blue|green|app)$ ]] || { printf 'invalid activation marker\n' >&2; exit 1; }
    elif service_running app; then old=app; fi
    candidate=blue
    [[ "$old" != blue ]] || candidate=green
    log "pulling candidate image and preparing partitions online"
    "${COMPOSE[@]}" pull --quiet "app-$candidate"
    "${COMPOSE[@]}" run --rm --no-deps "app-$candidate" /usr/local/bin/transitprepare -action prepare
    # An inactive slot may contain the previous rollback image. Replace only
    # that slot, never the currently serving container.
    "${COMPOSE[@]}" up -d --no-deps --force-recreate "app-$candidate"
    if ! wait_healthy "app-$candidate"; then
        "${COMPOSE[@]}" stop "app-$candidate"
        printf 'candidate failed health checks; previous app remains active\n' >&2
        exit 1
    fi
    # Probe real pages as well as process/DB health before accepting traffic.
    for path in / /transit/routes /transit/metrics /transit/method; do
        "${COMPOSE[@]}" exec -T "app-$candidate" wget -qO /dev/null "http://localhost:8080$path"
    done
    write_state prepared
    phase=prepared
fi

if [[ "$phase" == prepared ]]; then
    wait_healthy "app-$candidate" || { printf 'candidate is not healthy; previous app retained\n' >&2; exit 1; }
    if ! service_running caddy; then "${COMPOSE[@]}" up -d --no-deps caddy; fi
    log "switching HTTP to healthy $candidate slot"
    reload_candidate
    write_state routed
    phase=routed
fi
if [[ "$phase" == routed ]]; then
    log "stopping previous writer before schema cutover"
    if [[ "$old" == app ]]; then "${COMPOSE[@]}" stop -t 45 app
    elif [[ "$old" != none ]]; then "${COMPOSE[@]}" stop -t 45 "app-$old"; fi
    # docker compose stop waits for process exit. Never activate the new writer
    # while the old process can still commit recorder or background writes.
    if [[ "$old" != none ]]; then
        old_service=$old; [[ "$old" == app ]] || old_service="app-$old"
        if service_running "$old_service"; then printf 'old writer still running; refusing cutover\n' >&2; exit 1; fi
    fi
    write_state stopped
    phase=stopped
fi
# Resume with exactly the binary already checked and serving, even if the
# configured image tag moved while this deployment was interrupted.
candidate_id=$("${COMPOSE[@]}" ps -q "app-$candidate")
export TC_DEPLOY_IMAGE
candidate_image=$(docker inspect --format '{{.Image}}' "$candidate_id")
TC_DEPLOY_IMAGE=$candidate_image
log "finishing atomic partition/schema cutover; HTTP remains on read-only candidate"
if ! "${COMPOSE[@]}" run --rm --no-deps "app-$candidate" /usr/local/bin/transitprepare -action cutover; then
    printf 'cutover failed: candidate remains serving read-only, old writer remains stopped. Rerun deploy to resume; do not restart the old binary against an unknown schema.\n' >&2
    exit 1
fi
wait_healthy "app-$candidate" || { printf 'candidate unhealthy after cutover; writers remain disabled\n' >&2; exit 1; }
for path in /transit/routes /transit/metrics /transit/method; do
    "${COMPOSE[@]}" exec -T "app-$candidate" wget -qO /dev/null "http://localhost:8080$path"
done
log "activating $candidate background workers"
activate "$candidate"
rm .deploy/transition
rm -f .deploy/previous.Caddyfile
"${COMPOSE[@]}" ps
