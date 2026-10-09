#!/usr/bin/env bash
# Builds the image from the working tree and replaces the running container,
# then checks the page really answers. The data volume is never touched.
#   scripts/docker-up.sh          the monitor
#   scripts/docker-up.sh soak     the monitor and the soak sender
set -uo pipefail
cd "$(dirname "$0")/.."

# The shell's own variables win over .env, as they do for docker compose.
from_env() { [ -f .env ] && grep -E "^$1=" .env | tail -1 | cut -d= -f2-; }
http_port="${HM_HTTP_PORT:-$(from_env HM_HTTP_PORT)}"
http_port="${http_port:-8081}"
udp_port="${HM_UDP_PORT:-$(from_env HM_UDP_PORT)}"
udp_port="${udp_port:-8082}"
profile=()
[ "${1:-}" = "soak" ] && profile=(--profile soak)

say() { echo "docker-up: $*" >&2; }

# A server from `make dev` holds the same ports; Docker would then start the
# container without them. Only processes this user can see are ours to stop.
free_port() {
  local proto=$1 port=$2 pids
  pids="$(ss -H -"$proto"lnp "sport = :$port" 2>/dev/null | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u)"
  for pid in $pids; do
    say "port $port is held by pid $pid ($(ps -o args= -p "$pid" | cut -c1-80)); stopping it"
    pkill -f scripts/dev-server.sh 2>/dev/null
    kill -- -"$(ps -o pgid= -p "$pid" | tr -d ' ')" 2>/dev/null || kill "$pid" 2>/dev/null
  done
  for _ in $(seq 1 25); do
    [ -z "$(ss -H -"$proto"lnp "sport = :$port" 2>/dev/null | grep -o 'pid=[0-9]*')" ] && return 0
    sleep 0.2
  done
  say "port $port is still held; stop that process and run this again"
  exit 1
}

published() {
  docker compose port hm 8081 >/dev/null 2>&1 && docker compose port --protocol udp hm 8082 >/dev/null 2>&1
}

answers() {
  curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:$http_port/healthz" | grep -q 200
}

free_port t "$http_port"
free_port u "$udp_port"

export HM_COMMIT="${HM_COMMIT:-$(git describe --always --dirty --abbrev=7 2>/dev/null || echo none)}"
say "building the image $(cat VERSION 2>/dev/null) ($HM_COMMIT); the running container keeps serving meanwhile"
docker compose "${profile[@]}" build || exit 1

for attempt in 1 2; do
  say "starting the new container"
  docker compose "${profile[@]}" up -d --force-recreate --wait --wait-timeout 60 || true
  if published && answers; then
    say "up: http://localhost:$http_port (UDP intake on $udp_port)"
    if [ -z "${HM_PASS:-$(from_env HM_PASS)}" ] && [ -z "${HM_OPEN:-$(from_env HM_OPEN)}" ]; then
      say "log in with any name and the password \"default\"; change it with HM_PASS in .env, then make up"
    fi
    docker compose "${profile[@]}" ps --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}'
    exit 0
  fi
  say "attempt $attempt: the container runs but port $http_port does not answer; recreating once more"
  sleep 2
done

say "still not reachable. What Docker says:"
docker compose "${profile[@]}" ps --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}'
docker compose logs --tail 20 hm
exit 1
