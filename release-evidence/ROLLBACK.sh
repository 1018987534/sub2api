#!/bin/sh
set -eu

target=${1:?usage: ROLLBACK.sh <modified-file>}
original="${target}.orig"
test -f "$original"
cp "$original" "$target"
cmp -s "$target" "$original"
printf '%s\n' "restored behavior: $target matches $original"
