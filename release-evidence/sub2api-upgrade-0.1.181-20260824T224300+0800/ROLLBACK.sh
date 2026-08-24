#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  printf 'usage: %s TARGET_WORKTREE\n' "$0" >&2
  exit 2
fi

target=$1
baseline=f0c260c0e6662ae51ad1a95f988722d3ad84d9d6
modified=adb7bfb115d712124bc6153e0c3665fd9746d752

git -C "$target" diff --name-only -z "$baseline..$modified" |
  xargs -0 git -C "$target" restore --source "$baseline" --worktree --

printf 'rollback_result=backend_version=0.1.180\n'
