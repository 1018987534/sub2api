#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/common.sh"
PHASE=${1:-help}; shift || true
EXECUTE=false; CONFIRM=""; REVERSE=false
while (($#)); do
  case "$1" in
    --execute) EXECUTE=true ;;
    --confirm-cutover=*|--confirm-network=*|--confirm-finalize=*|--confirm-publish=*|--confirm-rollback=*|--confirm-close=*) CONFIRM=${1#*=} ;;
    --reverse-sync) REVERSE=true ;;
    -h|--help) PHASE=help ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done
case "$PHASE" in
  help)
    cat <<'EOF'
Usage: bwg_to_dmit.sh PHASE [OPTIONS]
  plan / preflight                    read only
  prepare --execute                   preflight -> stage -> full online PG rehearsal
  stage --execute                     pin live images; prepare isolated DMIT services
  seed --execute                      online PG rehearsal; no dump file retained
  cutover --execute --confirm-cutover=I_UNDERSTAND
                                      stop writes -> network -> final PG/Redis/Komari
                                      -> control -> FOUR gateways -> validate
  validate                            require all five nodes use DMIT data
  publish --execute --confirm-publish=I_UNDERSTAND
                                      move ONLY the two monitor DNS records
  finalize --execute --confirm-finalize=I_UNDERSTAND
                                      stop old data services, keep BWG gateway
  rollback --execute --confirm-rollback=I_UNDERSTAND [--reverse-sync]
                                      after any DMIT app start, reverse sync is required
  close --execute --confirm-close=I_UNDERSTAND
                                      end rollback window; erase temporary /run state
  wireguard --execute --confirm-network=I_UNDERSTAND
                                      recovery entry: refuses unless all apps stopped
EOF
    ;;
  plan)
    cat <<'EOF'
BWG stays gateway bwg-us-01; DMIT stays the sole control dmit-us-01.
DMIT 10.20.0.1 hosts PostgreSQL, Redis, Komari; BWG becomes 10.20.3.2.
VMISS-01/02 retain the YT UDP relay, whose upstream becomes DMIT.
No instance ID, application binary, Worker route or routing weight changes.
Before window: preflight -> stage -> seed (online rehearsal).
Window: stop/drain FIVE apps (90s grace) -> stop old Komari -> transfer network
-> final full PostgreSQL + globals -> exact manifests -> Redis RDB+AOF -> Komari
-> start DMIT control -> restart FOUR gateways serially -> validate.
After acceptance: publish monitoring DNS -> finalize old data services.
No historical backups copied. Config rollback state is temporary /run only.
After any target write, rollback requires stop writers and --reverse-sync.
EOF
    ;;
  preflight)
    for i in "${!host_list[@]}"; do
      say "inventory ${host_names[$i]}"
      remote_py "${host_list[$i]}" inventory "${host_names[$i]}" | tee "$EVIDENCE_DIR/${host_names[$i]}.json"
    done
    python3 - "$EVIDENCE_DIR" <<'PY'
import json,sys
from pathlib import Path
root=Path(sys.argv[1]);names=('bwg-us-01','vmiss-us-01','yt-us-01','vmiss-us-02','dmit-us-01')
nodes=[json.loads((root/(name+'.json')).read_text()) for name in names]
for name,node in zip(names,nodes):
    if node['ready']['checks']!={'database':'ok','redis':'ok'}:raise SystemExit(name+': shared services not ready')
    if node['app_env']['INSTANCE_ROLE']!=('control' if name=='dmit-us-01' else 'gateway'):raise SystemExit(name+': unexpected role')
    if node['architecture'] not in ('x86_64','amd64'):raise SystemExit(name+': review image architecture')
    if node['secret_fingerprints']!=nodes[0]['secret_fingerprints']:raise SystemExit(name+': shared credentials/encryption keys differ')
if nodes[0]['app_env']['DATABASE_HOST']!='postgres':raise SystemExit('BWG is no longer the original authority; use validate or inspect the recorded run')
print('five-node preflight=PASS; roles, architecture and shared encryption/credential fingerprints match')
PY
    python3 "$SCRIPT_DIR/dns.py" plan | tee "$EVIDENCE_DIR/dns-plan.json"
    say "preflight complete (read only): $EVIDENCE_DIR"
    ;;
  prepare)
    require_execute
    "$0" preflight
    "$0" stage --execute
    "$0" seed --execute
    say 'preparation complete; production still uses BWG; cutover/publish/finalize NOT executed'
    ;;
  stage)
    require_execute
    RUN_ID=$(remote_py "$BWG_SSH" existing-run)
    [[ -n "$RUN_ID" ]] || RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)-$$
    for host in "${host_list[@]}"; do
      install_helper "$host"
      installed_py "$host" snapshot "$RUN_ID"
    done
    installed_py "$BWG_SSH" source-services |
      installed_py "$DMIT_SSH" stage "$RUN_ID"
    for container in sub2api-postgres sub2api-redis komari; do
      image_id=$(remote "$BWG_SSH" "docker inspect '$container' --format '{{.Image}}'")
      if remote "$DMIT_SSH" "docker image inspect '$image_id' >/dev/null 2>&1"; then
        say "$container: exact source image already present"
      else
        remote "$BWG_SSH" "docker image save '$image_id'" | remote "$DMIT_SSH" 'docker image load --quiet'
      fi
    done
    installed_py "$DMIT_SSH" stage-start
    say 'stage prepared; production apps and BWG data untouched'
    ;;
  seed)
    require_execute; load_run
    installed_py "$DMIT_SSH" seed-guard "$RUN_ID"
    db_stream "$BWG_SSH" "$SOURCE_POSTGRES_CONTAINER" "$DMIT_SSH" "$TARGET_STAGE_PG_CONTAINER"
    installed_py "$DMIT_SSH" manifest "$TARGET_STAGE_PG_CONTAINER" > "$EVIDENCE_DIR/seed-target.json"
    installed_py "$DMIT_SSH" mark seeded "$RUN_ID"
    say 'seed restored all PostgreSQL schemas/data/roles; online source is still authoritative'
    say 'source may be changing; exact equality is checked after all five apps stop'
    ;;
  cutover|wireguard|validate|publish|finalize|rollback|close)
    args=()
    [[ "$EXECUTE" == true ]] && args+=(--execute)
    [[ -n "$CONFIRM" ]] && args+=("--confirm=$CONFIRM")
    [[ "$REVERSE" == true ]] && args+=(--reverse-sync)
    case "$PHASE" in
      wireguard) script=network_cutover.sh ;;
      publish) script=publish.sh ;;
      *) script=$PHASE.sh ;;
    esac
    exec "$SCRIPT_DIR/$script" "${args[@]}"
    ;;
  *) die "unknown phase: $PHASE" ;;
esac
