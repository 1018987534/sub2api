#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/common.sh"
EXECUTE=false; CONFIRM=""; REVERSE=false
for arg in "$@"; do
  case "$arg" in --execute) EXECUTE=true ;; --confirm=*) CONFIRM=${arg#*=} ;; --reverse-sync) REVERSE=true ;; *) die "unknown option: $arg" ;; esac
done
require_execute; require_confirm "$CONFIRM" rollback; load_run
writes=$(remote_py "$DMIT_SSH" writes-state)
[[ "$writes" == no || "$REVERSE" == true ]] || die 'DMIT may have written: add --reverse-sync to copy NEW data back before rollback'
for host in "${host_list[@]}"; do remote_py "$host" stop-app "$RUN_ID"; done
assert_apps_stopped
if [[ "$writes" == yes ]]; then
  say 'copying newest DMIT data BACK while ALL apps are stopped'
  remote_py "$DMIT_SSH" komari-stop
  remote_py "$BWG_SSH" source-data-start
  remote_py "$DMIT_SSH" writers-check "$TARGET_STAGE_PG_CONTAINER"
  db_stream "$DMIT_SSH" "$TARGET_STAGE_PG_CONTAINER" "$BWG_SSH" "$SOURCE_POSTGRES_CONTAINER"
  db_compare "$DMIT_SSH" "$TARGET_STAGE_PG_CONTAINER" "$BWG_SSH" "$SOURCE_POSTGRES_CONTAINER" reverse-db
  installed_py "$DMIT_SSH" redis-export "$TARGET_STAGE_REDIS_CONTAINER" |
    installed_py "$BWG_SSH" redis-import "$SOURCE_REDIS_CONTAINER"
  source_digest=$(remote_py "$DMIT_SSH" redis-export-hash)
  target_digest=$(remote_py "$BWG_SSH" redis-import-hash)
  [[ "$source_digest" == "$target_digest" ]] || die 'reverse Redis persistence manifest differs'
  remote "$DMIT_SSH" 'tar -C /opt/sub2api-migration-staging/komari_data -cf - .' |
    installed_py "$BWG_SSH" komari-import
fi
# If monitor DNS was partially/fully published, restore the exact recorded records.
python3 "$SCRIPT_DIR/dns.py" rollback
remote_py "$DMIT_SSH" komari-stop
remote_py "$DMIT_SSH" data-service remove
remote_py "$YT_SSH" relay restore
for host in "$BWG_SSH" "$YT_SSH" "$VMISS01_SSH" "$VMISS02_SSH" "$DMIT_SSH"; do
  remote_py "$host" wg-restore "$RUN_ID"
done
remote_py "$BWG_SSH" source-data-start
remote_py "$BWG_SSH" source-unfreeze
for i in "${!host_list[@]}"; do
  role=gateway; [[ ${host_names[$i]} == dmit-us-01 ]] && role=control
  remote_py "${host_list[$i]}" app-restore "$RUN_ID" "$role"
done
remote_py "$DMIT_SSH" monitor-restore
remote_py "$DMIT_SSH" firewall-remove
remote_py "$DMIT_SSH" stage-stop
remote_py "$DMIT_SSH" mark rollback-complete "$RUN_ID"
say 'rollback complete: BWG data authority restored, five apps ready; staging preserved for diagnosis'
