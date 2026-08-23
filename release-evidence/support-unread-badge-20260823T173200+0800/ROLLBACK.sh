#!/bin/bash
set -euo pipefail

BASE_SHA="0e63df60e1f608266af67e68cbf533854c0d51fb"
TARGET="${1:?usage: ROLLBACK.sh <git-worktree> [verification-file]}"
VERIFY_FILE="${2:-}"
GIT_BIN="${GIT_BIN:-/usr/bin/git}"

if [[ ! -d "$TARGET/.git" && ! -f "$TARGET/.git" ]]; then
  printf 'target is not a git worktree: %s\n' "$TARGET" >&2
  exit 2
fi

"$GIT_BIN" -C "$TARGET" restore --source "$BASE_SHA" -- \
  frontend/src/api/supportChat.ts \
  frontend/src/components/layout/AppSidebar.vue \
  frontend/src/components/layout/__tests__/AppSidebar.spec.ts \
  frontend/src/i18n/locales/en/common.ts \
  frontend/src/i18n/locales/zh/common.ts

if [[ -n "$VERIFY_FILE" ]]; then
  printf 'ROLLBACK_TARGET=%s\n' "$TARGET" >> "$VERIFY_FILE"
  "$GIT_BIN" -C "$TARGET" status --short --untracked-files=all >> "$VERIFY_FILE"
fi
