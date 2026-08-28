#!/bin/sh
set -eu

target=${1:?usage: ROLLBACK.sh <modified-file>}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cp "$script_dir/BASELINE_FILE" "$target"
cmp -s "$target" "$script_dir/BASELINE_FILE"
printf '%s\n' "restored behavior: $target matches $script_dir/BASELINE_FILE"
