#!/usr/bin/env bash
# Runs the Go server and restarts it whenever a .go file or config.yaml
# changes. Files under platforms/ are reloaded by the server itself, so saving
# a dashboard or a signal does not restart it. The Vite dev server reloads the
# browser by itself; the
# binary does not, and a stale server answering a newer page is a confusing
# way to lose an afternoon — it answers unknown routes with the SPA, so the
# browser gets a page where it expected data.
#
# No tooling to install: inotifywait when it is there, a mtime poll otherwise.
set -uo pipefail

cd "$(dirname "$0")/.."

pid=""
port="${HM_HTTP_ADDR:-:8081}"
port="${port##*:}"

# A server left over from an earlier session keeps the port, this one fails to
# bind, and the browser talks to a stale binary. Refuse, or take the port with
# HM_DEV_TAKEOVER=1.
guard_port() {
  local holder
  holder="$(ss -ltnpH "sport = :$port" 2>/dev/null | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2)"
  [ -n "$holder" ] || return 0
  if [ "${HM_DEV_TAKEOVER:-0}" = "1" ]; then
    echo "dev-server: port $port held by pid $holder ($(ps -o comm= -p "$holder")); stopping it" >&2
    kill "$holder" 2>/dev/null
    sleep 1
    return 0
  fi
  echo "dev-server: port $port is already held by pid $holder ($(ps -o args= -p "$holder"))." >&2
  echo "dev-server: stop it, or rerun with HM_DEV_TAKEOVER=1 to have it stopped for you." >&2
  exit 1
}

start() {
  guard_port
  go run ./cmd/hm &
  pid=$!
}

stop() {
  [ -n "$pid" ] || return 0
  # `go run` execs the binary as a child, so the whole group has to go or the
  # server keeps the port and the next start fails to bind.
  kill -- -"$pid" 2>/dev/null || kill "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null
  pid=""
  # The binary shuts down gracefully after `go run` has gone; wait for the port.
  for _ in $(seq 1 50); do
    [ -z "$(ss -ltnH "sport = :$port" 2>/dev/null)" ] && return 0
    sleep 0.2
  done
}

# HUP too: the server has its own process group, so a closed terminal misses it.
trap 'stop; exit 0' INT TERM HUP

if [ "${1:-}" = "--check" ]; then
  guard_port
  exit 0
fi

# The files worth restarting for. Anything under web/ is the dev server's job.
watched() {
  find cmd internal -type f -name '*.go' 2>/dev/null
  [ -f config.yaml ] && echo config.yaml
}

fingerprint() {
  watched | xargs -r stat -c '%n %Y' 2>/dev/null | sort | cksum
}

set -m # a process group per child, so stop() can take the whole tree
start
echo "dev-server: watching for changes (Ctrl-C stops)" >&2

last="$(fingerprint)"
while true; do
  if command -v inotifywait >/dev/null 2>&1; then
    inotifywait -qq -r -e modify,create,delete,move \
      --include '(\.go|config\.yaml)$' cmd internal config.yaml 2>/dev/null || sleep 1
  else
    sleep 1
  fi
  now="$(fingerprint)"
  [ "$now" = "$last" ] && continue
  last="$now"
  echo "dev-server: change detected, restarting" >&2
  stop
  start
done
