#!/bin/bash
set -euo pipefail

BASE_SHA="18a94e0df2bf8e8c4e899174293d6d6c3cb6afe1"
TARGET="${1:?usage: ROLLBACK.sh <git-worktree> [verification-file]}"
VERIFY_FILE="${2:-}"
GIT_BIN="${GIT_BIN:-/usr/bin/git}"

if [[ ! -d "$TARGET/.git" && ! -f "$TARGET/.git" ]]; then
  printf 'target is not a git worktree: %s\n' "$TARGET" >&2
  exit 2
fi

tracked=(
  backend/cmd/server/wire_gen.go
  backend/internal/handler/handler.go
  backend/internal/handler/wire.go
  backend/internal/server/routes/user.go
  backend/internal/server/routes/admin.go
  frontend/src/router/index.ts
  frontend/src/components/layout/AppSidebar.vue
  frontend/src/i18n/locales/zh/common.ts
  frontend/src/i18n/locales/en/common.ts
)
"$GIT_BIN" -C "$TARGET" restore --source "$BASE_SHA" -- "${tracked[@]}"
/bin/rm -f \
  "$TARGET/backend/internal/handler/support_chat_handler.go" \
  "$TARGET/backend/internal/handler/support_chat_handler_test.go" \
  "$TARGET/backend/migrations/230_support_chat.sql" \
  "$TARGET/backend/migrations/support_chat_migration_test.go" \
  "$TARGET/frontend/src/api/supportChat.ts" \
  "$TARGET/frontend/src/views/user/SupportChatView.vue" \
  "$TARGET/frontend/src/views/admin/SupportChatView.vue"

if [[ -n "$VERIFY_FILE" ]]; then
  printf 'ROLLBACK_TARGET=%s\n' "$TARGET" >> "$VERIFY_FILE"
  "$GIT_BIN" -C "$TARGET" status --short --untracked-files=all >> "$VERIFY_FILE"
fi
