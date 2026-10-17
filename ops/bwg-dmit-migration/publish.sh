#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/common.sh"
EXECUTE=false; CONFIRM=""
for arg in "$@"; do
  case "$arg" in --execute) EXECUTE=true ;; --confirm=*) CONFIRM=${arg#*=} ;; *) die "unknown option: $arg" ;; esac
done
require_execute; require_confirm "$CONFIRM" publish
"$SCRIPT_DIR/validate.sh"
python3 "$SCRIPT_DIR/dns.py" apply | tee "$EVIDENCE_DIR/dns-published.json"
python3 "$SCRIPT_DIR/dns.py" verify | tee "$EVIDENCE_DIR/dns-verified.json"
say 'two monitor DNS records now use DMIT; Worker and BWG gateway origin unchanged'
