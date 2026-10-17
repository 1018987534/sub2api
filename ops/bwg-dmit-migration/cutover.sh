#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/common.sh"
EXECUTE=false; CONFIRM=""
for arg in "$@"; do
  case "$arg" in --execute) EXECUTE=true ;; --confirm=*) CONFIRM=${arg#*=} ;; *) die "unknown option: $arg" ;; esac
done
require_execute; require_confirm "$CONFIRM" cutover; load_run
trap 'rc=$?; printf "cutover failed exit=%s; state preserved. Use rollback; add --reverse-sync if DMIT app was started.\n" "$rc" >&2; exit "$rc"' ERR
# Preflight every invariant while apps are still available; abort BEFORE downtime.
for host in "${host_list[@]}"; do remote_py "$host" drift-guard "$RUN_ID"; done
remote_py "$DMIT_SSH" cutover-guard "$RUN_ID"
python3 "$SCRIPT_DIR/dns.py" plan > "$EVIDENCE_DIR/dns-plan.json"
# Save only the source monitor TLS/vhosts, via SSH, before modifying networking.
installed_py "$BWG_SSH" monitor-export | installed_py "$DMIT_SSH" monitor-import
say 'stopping/draining all five application containers (90s each maximum)'
for host in "${host_list[@]}"; do remote_py "$host" stop-app "$RUN_ID"; done
assert_apps_stopped
remote_py "$BWG_SSH" source-freeze
"$SCRIPT_DIR/network_cutover.sh" --execute --confirm=I_UNDERSTAND
say 'final full PostgreSQL stream: source BWG -> target DMIT'
remote_py "$BWG_SSH" writers-check sub2api-postgres
# No --no-owner / --no-acl: owners, grants, every schema and all data are preserved.
db_stream "$BWG_SSH" "$SOURCE_POSTGRES_CONTAINER" "$DMIT_SSH" "$TARGET_STAGE_PG_CONTAINER"
db_compare "$BWG_SSH" "$SOURCE_POSTGRES_CONTAINER" "$DMIT_SSH" "$TARGET_STAGE_PG_CONTAINER" final-db
say 'transferring final Redis snapshot and complete Komari data'
remote_py "$BWG_SSH" redis-info "$SOURCE_REDIS_CONTAINER" > "$EVIDENCE_DIR/redis-source-before.json"
installed_py "$BWG_SSH" redis-export "$SOURCE_REDIS_CONTAINER" |
  installed_py "$DMIT_SSH" redis-import "$TARGET_STAGE_REDIS_CONTAINER"
remote_py "$DMIT_SSH" redis-info "$TARGET_STAGE_REDIS_CONTAINER" > "$EVIDENCE_DIR/redis-target-after.json"
source_digest=$(remote_py "$BWG_SSH" redis-export-hash)
target_digest=$(remote_py "$DMIT_SSH" redis-import-hash)
[[ "$source_digest" == "$target_digest" ]] || die 'Redis persistence manifest mismatch'
say "Redis full persistence manifest matched: $target_digest (DBSIZE may decrease as TTL expires)"
remote "$BWG_SSH" 'tar -C /opt/komari/data -cf - .' |
  remote "$DMIT_SSH" 'tar -C /opt/sub2api-migration-staging/komari_data -xf -'
remote_py "$BWG_SSH" tree-manifest /opt/komari/data > "$EVIDENCE_DIR/komari-source.json"
remote_py "$DMIT_SSH" tree-manifest /opt/sub2api-migration-staging/komari_data > "$EVIDENCE_DIR/komari-target.json"
diff -u "$EVIDENCE_DIR/komari-source.json" "$EVIDENCE_DIR/komari-target.json" > "$EVIDENCE_DIR/komari.diff"
remote_py "$DMIT_SSH" promote
remote_py "$DMIT_SSH" komari-start
remote_py "$DMIT_SSH" data-service install
# Any application start may run background writers. Mark before the first start.
remote_py "$DMIT_SSH" mark target-writes-possible "$RUN_ID"
remote_py "$DMIT_SSH" app-start "$RUN_ID" control
for i in 0 1 2 3; do remote_py "${host_list[$i]}" app-start "$RUN_ID" gateway; done
"$SCRIPT_DIR/validate.sh"
remote_py "$DMIT_SSH" mark cutover-complete "$RUN_ID"
say 'cutover complete; BWG remains gateway. Publish monitor DNS after acceptance.'
