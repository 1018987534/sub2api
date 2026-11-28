#!/usr/bin/env bash
# Loopback-only service exercise. Does not call SSH, Docker or Cloudflare.
set -euo pipefail
umask 077

pg_bin=${PG_BIN:-$(pg_config --bindir)}
for tool in "$pg_bin/initdb" "$pg_bin/pg_ctl" "$pg_bin/pg_basebackup" "$pg_bin/psql" "$pg_bin/createdb"; do
  test -x "$tool" || { printf 'PostgreSQL tool missing: %s\n' "$tool" >&2; exit 1; }
done
command -v redis-server >/dev/null
command -v redis-cli >/dev/null

root=$(mktemp -d /tmp/sub2api-ovh-fixture.XXXXXX)
pgsrc="$root/p0"
pgdst="$root/p1"
rsrc_pid=''
rdst_pid=''
cleanup() {
  for pid in "$rsrc_pid" "$rdst_pid"; do
    if [[ -n "$pid" ]]; then
      kill -TERM "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  for dir in "$pgdst" "$pgsrc"; do
    if [[ -f "$dir/postmaster.pid" ]]; then
      "$pg_bin/pg_ctl" -D "$dir" -m immediate -w stop >/dev/null || return 1
    fi
  done
  rm -rf -- "$root"
}
trap cleanup EXIT

read -r p0 p1 r0 r1 < <(python3 - <<'PY'
import socket
from contextlib import ExitStack

with ExitStack() as scope:
    sockets = [scope.enter_context(socket.socket()) for _ in range(4)]
    for listener in sockets:
        listener.bind(("127.0.0.1", 0))
    print(*[listener.getsockname()[1] for listener in sockets])
PY
)

pg() {
  local port=$1
  shift
  "$pg_bin/psql" -X -qAt -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$port" -U migration_fixture -d sub2api "$@"
}
start_pg() {
  "$pg_bin/pg_ctl" -w -D "$1" -l "$1/server.log" -o "-h 127.0.0.1 -k $root -p $2 -c wal_level=replica" start >/dev/null
}
cache() {
  local port=$1
  shift
  env -u REDISCLI_AUTH redis-cli -e --raw -h 127.0.0.1 -p "$port" "$@"
}
field() {
  cache "$1" INFO replication | tr -d '\r' | awk -F: -v k="$2" '$1 == k {print $2}'
}
until_true() {
  for ((i=0; i<100; i++)); do
    if "$@"; then return; fi
    sleep 0.2
  done
  printf 'Timed out checking local fixture: %s\n' "$1" >&2
  return 1
}
inside_redis() {
  cache "$1" INFO server 2>/dev/null | tr -d '\r' | rg -q "^process_id:$2$"
}

"$pg_bin/initdb" -D "$pgsrc" -U migration_fixture --auth-local=trust --auth-host=trust --no-locale >/dev/null
start_pg "$pgsrc" "$p0"
"$pg_bin/createdb" -h 127.0.0.1 -p "$p0" -U migration_fixture sub2api
pg "$p0" -c 'CREATE TABLE fixture (value text);' >/dev/null
"$pg_bin/pg_basebackup" -D "$pgdst" -h 127.0.0.1 -p "$p0" -U migration_fixture -X stream -R --checkpoint=fast
start_pg "$pgdst" "$p1"
pg "$p0" -c "INSERT INTO fixture VALUES ('after-basebackup'); CHECKPOINT;" >/dev/null
frozen_lsn=$(pg "$p0" -c 'SELECT pg_current_wal_flush_lsn();')
replayed() { [[ $(pg "$p1" -c "SELECT pg_is_in_recovery() AND pg_last_wal_replay_lsn() >= '$frozen_lsn'::pg_lsn;") == t ]]; }
until_true replayed
"$pg_bin/pg_ctl" -D "$pgsrc" -m fast -w stop >/dev/null
[[ $(pg "$p1" -c 'SELECT pg_promote(true, 10);') == t ]]
[[ $(pg "$p1" -c "SELECT value FROM fixture;") == after-basebackup ]]
pg "$p1" -c "INSERT INTO fixture VALUES ('after-promotion');" >/dev/null
printf 'PostgreSQL: replayed the frozen LSN, fenced source, retained data, accepted new writes.\n'

mkdir "$root/r0" "$root/r1"
redis-server --bind 127.0.0.1 --protected-mode yes --port "$r0" --dir "$root/r0" --save '' --appendonly yes >"$root/r0/redis.log" 2>&1 &
rsrc_pid=$!
until_true inside_redis "$r0" "$rsrc_pid"
redis-server --bind 127.0.0.1 --protected-mode yes --port "$r1" --dir "$root/r1" --save '' --appendonly yes \
  --replicaof 127.0.0.1 "$r0" >"$root/r1/redis.log" 2>&1 &
rdst_pid=$!
until_true inside_redis "$r1" "$rdst_pid"
replicating() { [[ $(field "$r1" master_link_status) == up ]]; }
until_true replicating
[[ $(cache "$r0" SET fixture before-promotion EX 60) == OK ]]
redis_offset=$(field "$r0" master_repl_offset)
redis_id=$(field "$r0" master_replid)
redis_replayed() {
  local offset
  offset=$(field "$r1" slave_repl_offset)
  [[ $(field "$r1" master_replid) == "$redis_id" && "$offset" =~ ^[0-9]+$ && "$offset" -ge "$redis_offset" ]]
}
until_true redis_replayed
kill -TERM "$rsrc_pid"
wait "$rsrc_pid"
rsrc_pid=''
[[ $(cache "$r1" REPLICAOF NO ONE) == OK && $(field "$r1" role) == master ]]
[[ $(cache "$r1" GET fixture) == before-promotion ]]
[[ $(cache "$r1" TTL fixture) -gt 0 ]]
[[ $(cache "$r1" SET fixture-after-promotion ok) == OK ]]
printf 'Redis: matched replication ID and offset, fenced source, retained data and TTL, accepted new writes.\n'
