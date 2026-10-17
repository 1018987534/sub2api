#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/common.sh"
[[ $# == 0 ]] || die 'validate takes no mutation options'
load_run
: > "$EVIDENCE_DIR/validation.txt"
for i in "${!host_list[@]}"; do
  remote_py "${host_list[$i]}" validate "${host_names[$i]}" | tee -a "$EVIDENCE_DIR/validation.txt"
done
remote_py "$DMIT_SSH" validate-data | tee -a "$EVIDENCE_DIR/validation.txt"
for path in /health /health/ready /api/v1/settings/public /admin/accounts; do
  curl --suppress-connect-headers -fsS --max-time 25 -o /dev/null -w "xiaohondou$path=%{http_code}\n" "https://xiaohondou.com$path" | tee -a "$EVIDENCE_DIR/validation.txt"
done
# Auth boundary and live routing runtime, read from local control without logging tokens.
remote_py "$DMIT_SSH" runtime-check | tee -a "$EVIDENCE_DIR/validation.txt"
say 'all five roles and DMIT data verified; no paid/Responses POST was generated'
