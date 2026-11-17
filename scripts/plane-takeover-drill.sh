#!/usr/bin/env bash
# Plane takeover drill (#700, ADR 0089). Starts two plane instances on one
# throwaway PostgreSQL store, has instance A hold the scheduler lease, a
# schedule and a leased turn, kills A without draining it, and times what B
# takes over. Prints one line per event with seconds since the kill.
#
# Usage: scripts/plane-takeover-drill.sh [path/to/rusui]
# Needs docker (or podman as docker), curl and python3. Nothing leaves the
# machine: GitHub calls go to a closed local port and every port is
# ephemeral, so concurrent drills do not collide.
set -euo pipefail

bin=${1:-}
work=$(mktemp -d "${TMPDIR:-/tmp}/rusui-drill-$$-XXXXXX")
if [ -z "$bin" ]; then
	bin="$work/rusui"
	go build -o "$bin" ./cmd/rusui
fi
db="rusui-drill-$$-$RANDOM"
pids=()
cleanup() {
	for p in "${pids[@]}"; do kill -9 "$p" 2>/dev/null || true; done
	docker rm -f "$db" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

t0=""
log() {
	local at=""
	[ -n "$t0" ] && at=$(python3 -c "import time; print(f'+{time.time()-$t0:.1f}s')")
	echo "$(date -u +%H:%M:%S) ${at:-start} $*"
}
port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'; }
sql() { docker exec "$db" psql -U postgres -tAc "$1"; }
field() { python3 -c "import json, sys; print(json.load(sys.stdin)['$1'])"; }

docker run -d --rm --name "$db" -e POSTGRES_PASSWORD=drill -p 127.0.0.1:0:5432 \
	postgres:17 -c synchronous_commit=on >/dev/null
pg=$(docker port "$db" 5432 | head -1 | cut -d: -f2)
for _ in $(seq 60); do docker exec "$db" pg_isready -q -h 127.0.0.1 && break; sleep 1; done

cat >"$work/policy.yaml" <<'EOF'
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [run, scheduled]
  egress: trusted
projects:
  drill:
    repos:
      example/drill:
        visibility: public
EOF

export RUSUI_STORE_URL="postgres://postgres:drill@127.0.0.1:$pg/postgres?sslmode=disable"
RUSUI_WORKER_SECRET=$(python3 -c 'import secrets; print(secrets.token_hex(16))')
export RUSUI_WORKER_SECRET
export RUSUI_WEBHOOK_SECRET=drill-webhook RUSUI_SLACK_SECRET=drill-slack
export RUSUI_GITHUB_TOKEN=drill RUSUI_GITHUB_API=http://127.0.0.1:9
auth=(-H "Authorization: Bearer $RUSUI_WORKER_SECRET" -H 'Content-Type: application/json')

# start NAME: run one instance; sets $addr and $pid.
start() {
	addr=127.0.0.1:$(port)
	mkdir -p "$work/$1"
	"$bin" -addr "$addr" -db "$work/$1/rusui.db" -policy "$work/policy.yaml" >"$work/$1.log" 2>&1 &
	pid=$!
	pids+=("$pid")
	# /readyz also checks guest setup this drill does not provide; wait
	# for the listener instead.
	for _ in $(seq 120); do curl -s -o /dev/null "http://$addr/readyz" && return; sleep 0.5; done
	echo "instance $1 did not start" >&2
	cat "$work/$1.log" >&2
	exit 1
}

start a
a=$addr a_pid=$pid
log "A serving; scheduler lease: $(sql "SELECT 'gen '||generation FROM plane_job_leases WHERE name='scheduler'")"
start b
b=$addr
log "B serving"

curl -sf "${auth[@]}" -d '{"name":"drill","every":"1m","prompt":"scheduled drill work"}' "http://$a/projects/drill/schedules" >/dev/null
curl -sf "${auth[@]}" -d '{"kind":"run","prompt":"turn leased through A"}' "http://$a/projects/drill/sessions" >/dev/null
job=$(curl -sf "${auth[@]}" -d '{"repo":"example/drill","claim_token":"drill-a"}' "http://$a/jobs/claim" | field job_id)
log "A leased turn $job"
acked=$(curl -sf "${auth[@]}" -d '{"kind":"run","prompt":"acknowledged before the kill"}' "http://$a/projects/drill/sessions" | field session_id)
log "A acknowledged session $acked"

sleep 5
kill -9 "$a_pid"
t0=$(python3 -c 'import time; print(time.time())')
log "killed A"

gen0=$(sql "SELECT generation FROM plane_job_leases WHERE name='scheduler'")
fires0=$(sql "SELECT COUNT(*) FROM sessions WHERE kind='scheduled'")
lease="" fire="" turn=""
for _ in $(seq 600); do
	if [ -z "$lease" ] && [ "$(sql "SELECT generation FROM plane_job_leases WHERE name='scheduler'")" != "$gen0" ]; then
		lease=1
		log "B holds the scheduler lease"
	fi
	if [ -z "$fire" ] && [ "$(sql "SELECT COUNT(*) FROM sessions WHERE kind='scheduled'")" != "$fires0" ]; then
		fire=1
		log "schedule fired on B"
	fi
	if [ -z "$turn" ]; then
		got=$(curl -s "${auth[@]}" -d "{\"repo\":\"example/drill\",\"claim_token\":\"drill-b-$RANDOM\"}" "http://$b/jobs/claim" || true)
		if [ -n "$got" ] && [ "$(echo "$got" | field job_id)" = "$job" ]; then
			turn=1
			log "B granted turn $job again at generation $(echo "$got" | field lease_generation)"
		fi
	fi
	[ -n "$lease" ] && [ -n "$fire" ] && [ -n "$turn" ] && break
	sleep 1
done
if [ -z "$lease" ] || [ -z "$fire" ] || [ -z "$turn" ]; then
	log "takeover incomplete after 600s"
	exit 1
fi

kept=$(sql "SELECT COUNT(*) FROM sessions WHERE id=$acked")
fires=$(sql "SELECT COUNT(*) FROM sessions WHERE kind='scheduled'")
log "acknowledged session $acked kept: $kept; scheduled sessions: $fires"
[ "$kept" = 1 ] || exit 1
