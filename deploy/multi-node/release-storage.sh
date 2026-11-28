#!/usr/bin/env bash
set -euo pipefail

# Run over the already authorized SSH route, on one Sub2API node.
mode=${1:?expected check-space, plan-cleanup or cleanup}
archive=${2:?expected archive directory}
if [[ ! "$archive" =~ ^/[a-zA-Z0-9._/-]+/patched-binaries$ || ! -d "$archive" || "$(realpath -e -- "$archive")" != "$archive" ]]; then
  echo 'Invalid Sub2API archive directory' >&2
  exit 2
fi

available() { df -B1 --output=avail -- "$archive" | awk 'NR == 2 { print $1 }'; }
archive_usage() { du -sx -B1 -- "$archive" | cut -f1; }
checksum() { sha256sum -- "$1" | awk '{print $1}'; }
regular_file() { [[ -f "$1" && ! -L "$1" ]]; }

if [[ "$mode" == check-space ]]; then
  required=${3:?expected number of incoming bytes}
  [[ "$required" =~ ^[1-9][0-9]*$ ]]
  free=$(available)
  printf 'available_before_bytes=%s required_staging_bytes=%s archive_usage_bytes=%s\n' "$free" "$required" "$(archive_usage)"
  if (( free < required )); then
    echo 'Insufficient space for the new Sub2API artifact' >&2
    exit 1
  fi
  exit
fi
[[ "$mode" == plan-cleanup || "$mode" == cleanup ]]
version=${3:?expected active version}
commit=${4:?expected active short SHA}
live_hash=${5:?expected live SHA256}
rollback_hash=${6:?expected rollback SHA256}
rollback_price=${7:?expected rollback pricing SHA256 or - for unchanged resources}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$commit" =~ ^[0-9a-f]{10}$ ]]
[[ "$live_hash" =~ ^[0-9a-f]{64}$ && "$rollback_hash" =~ ^[0-9a-f]{64}$ && "$live_hash" != "$rollback_hash" ]]
[[ "$rollback_price" == - || "$rollback_price" =~ ^[0-9a-f]{64}$ ]]
rollback="$archive/sub2api-before-$version-$commit"
resource_rollback="$archive/resources-before-$version-$commit.tgz"
price_path=resources/model-pricing/model_prices_and_context_window.json

exec 8>"$archive/.codex-app-release.lock"
flock -n 8
mount=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/sub2api"}}{{.Source}}{{end}}{{end}}' sub2api)
[[ "${mount%/*}" == "$archive" && "$mount" != "$rollback" && "$mount" != "$resource_rollback" ]]

verify_keep_set() {
  local pid version_line
  [[ "$(docker inspect -f '{{.State.Health.Status}}' sub2api)" == healthy ]] || return 1
  curl --noproxy '*' -fsS --max-time 8 http://127.0.0.1:8080/health/ready >/dev/null || return 1
  pid=$(docker top sub2api -eo pid,comm | awk '$2 == "sub2api" {print $1}') || return 1
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  regular_file "$mount" && [[ "$(checksum "$mount")" == "$live_hash" && "$(checksum "/proc/$pid/exe")" == "$live_hash" ]] || return 1
  version_line=$(docker exec sub2api /app/sub2api --version 2>&1) || return 1
  [[ "$version_line" == *"Sub2API $version (commit: $commit,"* ]] || return 1
  regular_file "$rollback" && [[ -x "$rollback" && "$(checksum "$rollback")" == "$rollback_hash" ]] || return 1
  if [[ "$rollback_price" != - ]]; then
    regular_file "$resource_rollback" || return 1
    [[ "$(tar -xOzf "$resource_rollback" "$price_path" | sha256sum | awk '{print $1}')" == "$rollback_price" ]] || return 1
  fi
}

verify_keep_set
container_ids=$(docker ps -aq)
[[ -n "$container_ids" ]]
# A stopped container can still reference a host file and be started later.
container_mounts=$(printf '%s\n' "$container_ids" | xargs docker inspect -f '{{range .Mounts}}{{println .Source}}{{end}}')
is_mounted() {
  local source
  while IFS= read -r source; do
    [[ -n "$source" ]] || continue
    if [[ "$source" == "$1" || "$source" == "$1/"* || "$1" == "${source%/}/"* ]]; then
      return 0
    fi
  done <<< "$container_mounts"
  return 1
}

printf 'preserve=%s\npreserve=%s\n' "$mount" "$rollback"
if [[ "$rollback_price" != - ]]; then printf 'preserve=%s\n' "$resource_rollback"; fi
printf 'available_before_bytes=%s archive_before_bytes=%s\n' "$(available)" "$(archive_usage)"
removed=0
for candidate in "$archive"/sub2api-* "$archive"/resources-before-*; do
  regular_file "$candidate" || continue
  if [[ "$candidate" == "$mount" || "$candidate" == "$rollback" || ( "$rollback_price" != - && "$candidate" == "$resource_rollback" ) ]]; then continue; fi
  name=${candidate##*/}
  if [[ ! "$name" =~ ^sub2api-(before-)?[0-9]+\.[0-9]+\.[0-9]+-[0-9a-f]{7,14}(-(control|gateway))?$ && ! "$name" =~ ^resources-before-[0-9]+\.[0-9]+\.[0-9]+-[0-9a-f]{7,14}\.tgz$ ]]; then continue; fi
  if is_mounted "$candidate"; then printf 'preserve_mounted=%s\n' "$candidate"; continue; fi
  printf '%s=%s\n' "$mode" "$candidate"
  if [[ "$mode" == cleanup ]]; then
    rm -- "$candidate"
    removed=$((removed + 1))
  fi
done

# Old release-owned staging directories contain only these known files.
shopt -s nullglob dotglob
for stage in "$archive"/.stage-*; do
  name=${stage##*/}
  [[ -d "$stage" && ! -L "$stage" && "$name" =~ ^\.stage-[0-9a-f]{10}(\.[a-zA-Z0-9]{6})?$ ]] || continue
  [[ "$name" != ".stage-$commit" && "$name" != ".stage-$commit."* ]] || continue
  if is_mounted "$stage"; then printf 'preserve_mounted=%s\n' "$stage"; continue; fi
  # Staging and activation use separate SSH calls. Recent stages may still
  # belong to an operator holding a verified artifact for the next switch.
  if (( $(date +%s) - $(stat -c %Y -- "$stage") < 24 * 60 * 60 )); then continue; fi
  entries=("$stage"/*)
  for entry in "${entries[@]}"; do
    check=${entry##*/}
    if ! regular_file "$entry" || [[ "$check" != binary && "$check" != pricing.json && "$check" != README.md && "$check" != model_prices_and_context_window.json && "$check" != previous-readme && "$check" != previous-pricing ]]; then
      printf 'preserve_unrecognized_stage=%s\n' "$stage"
      continue 2
    fi
  done
  printf '%s_stage=%s\n' "$mode" "$stage"
  if [[ "$mode" == cleanup ]]; then
    if ((${#entries[@]})); then rm -- "${entries[@]}"; fi
    rmdir -- "$stage"
    removed=$((removed + 1))
  fi
done

verify_keep_set
printf 'removed_entries=%s available_after_bytes=%s archive_after_bytes=%s\n' "$removed" "$(available)" "$(archive_usage)"
