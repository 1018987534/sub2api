#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="${MIGRATION_CONFIG:-$SCRIPT_DIR/migration.env}"
set -a
. "$CONFIG_FILE"
set +a
SSH_OPTS=(-i "${MIGRATION_SSH_KEY:-$HOME/.ssh/id_ed25519}" -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectTimeout=12 -o ServerAliveInterval=15 -o ServerAliveCountMax=4)
EVIDENCE_DIR="${MIGRATION_EVIDENCE_DIR:-$HOME/Documents/Codex/bwg-dmit-migration/$(date -u +%Y%m%dT%H%M%SZ)-$$}"
mkdir -p "$EVIDENCE_DIR"
chmod 700 "$EVIDENCE_DIR"
export MIGRATION_EVIDENCE_DIR="$EVIDENCE_DIR"
host_list=("$BWG_SSH" "$VMISS01_SSH" "$YT_SSH" "$VMISS02_SSH" "$DMIT_SSH")
host_names=(bwg-us-01 vmiss-us-01 yt-us-01 vmiss-us-02 dmit-us-01)
say() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
remote() { local host=$1; shift; ssh "${SSH_OPTS[@]}" "$host" "$@"; }
remote_py() {
  local host=$1 command_text; shift
  printf -v command_text '%q ' python3 - "$@"
  remote "$host" "$command_text" < "$SCRIPT_DIR/remote.py"
}
installed_py() {
  local host=$1 command_text; shift
  printf -v command_text '%q ' python3 /run/bwg-dmit-migration/remote.py "$@"
  remote "$host" "$command_text"
}
install_helper() {
  remote "$1" 'umask 077; mkdir -p /run/bwg-dmit-migration; cat > /run/bwg-dmit-migration/remote.py' < "$SCRIPT_DIR/remote.py"
}
require_execute() { [[ "${EXECUTE:-false}" == true ]] || die 'add --execute; no remote changes made'; }
require_confirm() { [[ "$1" == I_UNDERSTAND ]] || die "add --confirm-$2=I_UNDERSTAND"; }
assert_apps_stopped() {
  local host
  for host in "${host_list[@]}"; do remote_py "$host" stopped "$RUN_ID" >/dev/null; done
}
db_stream() {
  local source_host=$1 source_container=$2 target_host=$3 target_container=$4
  installed_py "$source_host" globals-export "$source_container" |
    installed_py "$target_host" globals-import "$target_container"
  installed_py "$target_host" db-reset "$target_container"
  installed_py "$source_host" dump "$source_container" |
    installed_py "$target_host" restore "$target_container"
}
db_compare() {
  local source_raw target_raw source_canonical='' target_canonical=''
  installed_py "$1" manifest "$2" > "$EVIDENCE_DIR/$5-source.json"
  installed_py "$3" manifest "$4" > "$EVIDENCE_DIR/$5-target.json"
  source_raw=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["schema_sha256"])' "$EVIDENCE_DIR/$5-source.json")
  target_raw=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["schema_sha256"])' "$EVIDENCE_DIR/$5-target.json")
  if [[ "$source_raw" != "$target_raw" ]]; then
    source_canonical=$(installed_py "$1" schema-export "$2" | installed_py "$DMIT_SSH" schema-normalize "$TARGET_STAGE_PG_CONTAINER")
    target_canonical=$(installed_py "$3" schema-export "$4" | installed_py "$DMIT_SSH" schema-normalize "$TARGET_STAGE_PG_CONTAINER")
    [[ "$source_canonical" == "$target_canonical" ]] || die 'complete canonical schemas differ; constraints/owners/ACL are not equivalent'
    say 'raw schema formatting differs; both complete schemas reparse identically in isolated DMIT scratch databases'
  fi
  python3 - "$EVIDENCE_DIR/$5-source.json" "$EVIDENCE_DIR/$5-target.json" "$source_canonical" "$target_canonical" "$EVIDENCE_DIR/$5" <<'PY'
import json,sys,difflib
source=json.load(open(sys.argv[1]));target=json.load(open(sys.argv[2]))
if source['schema_sha256']!=target['schema_sha256']:
    if not sys.argv[3] or sys.argv[3]!=sys.argv[4]:raise SystemExit('canonical schema proof missing or different')
    proof={'source_raw':source['schema_sha256'],'target_raw':target['schema_sha256'],'source_canonical':sys.argv[3],'target_canonical':sys.argv[4]}
    open(sys.argv[5]+'-schema-proof.json','w').write(json.dumps(proof,indent=2)+'\n')
    source['schema_sha256']=sys.argv[3];target['schema_sha256']=sys.argv[4]
delta=''.join(difflib.unified_diff(json.dumps(source,sort_keys=True,indent=2).splitlines(True),json.dumps(target,sort_keys=True,indent=2).splitlines(True)))
open(sys.argv[5]+'.diff','w').write(delta)
if delta:raise SystemExit('full database manifest differs: '+sys.argv[5]+'.diff')
PY
  say "$5: all tables, row hashes, sequences, schema, roles and large objects match"
}
load_run() {
  RUN_ID=$(remote_py "$DMIT_SSH" run-id)
  [[ "$RUN_ID" =~ ^[0-9TZ-]+$ ]] || die 'missing/invalid prepared run; run stage first'
  export RUN_ID
}
