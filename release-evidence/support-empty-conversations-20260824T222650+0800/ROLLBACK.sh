#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  printf 'usage: %s TARGET_COPY\n' "$0" >&2
  exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cp "$script_dir/ORIGINAL_FILE" "$1"
printf 'rollback_result=requires_existing_message=false\n'
