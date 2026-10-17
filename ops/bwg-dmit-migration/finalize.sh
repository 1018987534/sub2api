#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/common.sh"
EXECUTE=false; CONFIRM=""
for arg in "$@"; do
  case "$arg" in --execute) EXECUTE=true ;; --confirm=*) CONFIRM=${arg#*=} ;; *) die "unknown option: $arg" ;; esac
done
require_execute; require_confirm "$CONFIRM" finalize; load_run
"$SCRIPT_DIR/validate.sh"
python3 "$SCRIPT_DIR/dns.py" verify | tee "$EVIDENCE_DIR/dns-verified.json"
for domain in $MONITOR_DOMAINS; do
  curl --suppress-connect-headers -fsS --max-time 25 -o /dev/null -w "$domain=%{http_code}\n" "https://$domain/api/nodes"
done
# Keep run state for reverse-sync rollback; remove it only with the explicit cleanup phase.
remote_py "$BWG_SSH" source-finalize
remote_py "$DMIT_SSH" cert-renew
remote_py "$DMIT_SSH" mark finalized "$RUN_ID"
say 'old PG/Redis/relays/Komari stopped with restart disabled; BWG gateway still ready'
say 'source online data retained; no historical backup copied; temporary /run state kept until rollback window ends'
