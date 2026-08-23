#!/bin/bash
set -euo pipefail

BASE_SHA="5ff4b03816fe1e66e4580d067e9f33bcfe4d7e7d"
TARGET="${1:?usage: ROLLBACK.sh <git-worktree> [verification-file]}"
VERIFY_FILE="${2:-}"
GIT_BIN="${GIT_BIN:-/usr/bin/git}"

if [[ ! -d "$TARGET/.git" && ! -f "$TARGET/.git" ]]; then
  printf 'target is not a git worktree: %s\n' "$TARGET" >&2
  exit 2
fi

"$GIT_BIN" -C "$TARGET" restore --source "$BASE_SHA" -- \
  backend/internal/handler/support_chat_handler.go \
  backend/internal/handler/support_chat_handler_test.go \
  frontend/src/api/supportChat.ts \
  frontend/src/views/admin/SupportChatView.vue \
  frontend/src/views/user/SupportChatView.vue

if [[ -n "$VERIFY_FILE" ]]; then
  printf 'ROLLBACK_TARGET=%s\n' "$TARGET" >> "$VERIFY_FILE"
  "$GIT_BIN" -C "$TARGET" status --short --untracked-files=all >> "$VERIFY_FILE"
fi
