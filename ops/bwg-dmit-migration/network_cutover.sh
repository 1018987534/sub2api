#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/common.sh"
EXECUTE=false; CONFIRM=""
for arg in "$@"; do
  case "$arg" in
    --execute) EXECUTE=true ;;
    --confirm=*) CONFIRM=${arg#*=} ;;
    *) die "unknown option: $arg" ;;
  esac
done
require_execute; require_confirm "$CONFIRM" network; load_run
# Check every host before the FIRST network or firewall write.
assert_apps_stopped
say 'all five apps stopped in this run; preparing network'
bwg_key=$(remote_py "$BWG_SSH" public-key)
vm1_key=$(remote_py "$VMISS01_SSH" public-key)
yt_key=$(remote_py "$YT_SSH" public-key)
vm2_key=$(remote_py "$VMISS02_SSH" public-key)
dmit_key=$(remote_py "$DMIT_SSH" public-key)
hub_peers=$(python3 - "$bwg_key" "$vm1_key" "$yt_key" "$vm2_key" <<'PY'
import json,sys
print(json.dumps([{'key':k,'address':a} for k,a in zip(sys.argv[1:],['10.20.3.2','10.20.0.2','10.20.1.2','10.20.2.2'])]))
PY
)
peer=$(python3 - "$dmit_key" <<'PY'
import json,sys
print(json.dumps([{'key':sys.argv[1],'address':'10.20.0.1'}]))
PY
)
remote_py "$DMIT_SSH" wg-prepare "$RUN_ID" 10.20.0.1/32 hub "$hub_peers"
remote_py "$BWG_SSH" wg-prepare "$RUN_ID" 10.20.3.2/32 "$DMIT_PUBLIC_IP:$WG_PORT" "$peer"
remote_py "$YT_SSH" wg-prepare "$RUN_ID" 10.20.1.2/32 "$DMIT_PUBLIC_IP:$WG_PORT" "$peer"
remote_py "$VMISS01_SSH" wg-prepare "$RUN_ID" 10.20.0.2/32 "$YT_PUBLIC_IP:$WG_RELAY_PORT" "$peer"
remote_py "$VMISS02_SSH" wg-prepare "$RUN_ID" 10.20.2.2/32 "$YT_PUBLIC_IP:$WG_RELAY_PORT" "$peer"
remote_py "$DMIT_SSH" firewall-install
remote_py "$DMIT_SSH" wg-apply "$RUN_ID"
remote_py "$BWG_SSH" wg-apply "$RUN_ID"
remote_py "$YT_SSH" wg-apply "$RUN_ID"
remote_py "$YT_SSH" relay apply
remote_py "$VMISS01_SSH" wg-apply "$RUN_ID"
remote_py "$VMISS02_SSH" wg-apply "$RUN_ID"
for i in 0 1 2 3; do
  remote_py "${host_list[$i]}" network-check "${host_names[$i]}" | tee -a "$EVIDENCE_DIR/network.txt"
done
remote_py "$DMIT_SSH" mark network-ready "$RUN_ID"
say 'network ready; DMIT hub, BWG/YT direct, two VMISS through YT; BWG is no longer a dependency'
