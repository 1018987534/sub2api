#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/common.sh"
EXECUTE=false; CONFIRM=""
for arg in "$@"; do
  case "$arg" in --execute) EXECUTE=true ;; --confirm=*) CONFIRM=${arg#*=} ;; *) die "unknown option: $arg" ;; esac
done
require_execute; require_confirm "$CONFIRM" close; load_run
"$SCRIPT_DIR/validate.sh"
remote "$DMIT_SSH" "test -f /run/bwg-dmit-migration/finalized"
for host in "${host_list[@]}"; do remote_py "$host" cleanup-state "$RUN_ID"; done
say 'temporary rollback configuration erased; DMIT live data and BWG gateway preserved'
