#!/bin/sh
set -eu
target="${1:?usage: ROLLBACK.sh TARGET}"
tmp="${target}.rollback-tmp"
sed \
  -e 's/^changed_branch=.*/changed_branch=legacy_single_group/' \
  -e 's/^changed_field=.*/changed_field=group_id/' \
  -e 's/^route_order=.*/route_order=primary/' \
  -e 's/^max_rate_multiplier=.*/max_rate_multiplier=unlimited/' \
  -e 's/^fallback_policy=.*/fallback_policy=none/' \
  "$target" > "$tmp"
mv "$tmp" "$target"
printf '%s\n' 'ROLLBACK_OK restored_legacy_single_group_routing'
