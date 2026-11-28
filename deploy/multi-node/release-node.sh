#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

id=${1:?node ID}
role=${2:?control or gateway}
archive=${3:?release archive directory}
version=${4:?version}
commit=${5:?short commit}
expected=${6:?candidate binary SHA256}
new_readme=${7:?candidate README SHA256}
new_pricing=${8:?candidate pricing SHA256}
stage=${9:?candidate staging directory}
[[ "$role" == control || "$role" == gateway ]]
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$commit" =~ ^[a-f0-9]{10}$ ]]
for hash in "$expected" "$new_readme" "$new_pricing"; do [[ "$hash" =~ ^[a-f0-9]{64}$ ]]; done
[[ "$archive" =~ ^/[a-zA-Z0-9._/-]+/patched-binaries$ && "$(realpath -e -- "$archive")" == "$archive" ]]
[[ "${stage%/*}" == "$archive" && "${stage##*/}" =~ ^\.stage-${commit}\.[a-zA-Z0-9]{6}$ && -d "$stage" && ! -L "$stage" ]]
[[ "$(uname -m)" == x86_64 ]]

trap 'rm -rf -- "$stage"' EXIT
exec 8>"$archive/.codex-app-release.lock"
flock -n 8
container=sub2api
resources=/app/resources
readme=model-pricing/README.md
pricing=model-pricing/model_prices_and_context_window.json
backup="$archive/sub2api-before-$version-$commit"
resource_backup="$archive/resources-before-$version-$commit.tgz"
mount=$(docker inspect -f '{{range .Mounts}}{{if and (eq .Destination "/app/sub2api") (eq .Type "bind")}}{{.Source}}{{end}}{{end}}' "$container")
[[ "${mount%/*}" == "$archive" && -f "$mount" && ! -L "$mount" && -x "$mount" && "$mount" != "$backup" ]]
resource_mount=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/resources"}}{{.Source}}{{end}}{{end}}' "$container")
if [[ -n "$resource_mount" ]]; then
  [[ "$resource_mount" == "${archive%/*}/resources" && "$(realpath -e -- "$resource_mount")" == "$resource_mount" ]]
fi

checksum() { sha256sum -- "$1" | awk '{print $1}'; }
container_hash() { docker exec "$container" sha256sum "$1" | awk '{print $1}'; }
active_hash() {
  local pid
  pid=$(docker top "$container" -eo pid,comm | awk '$2=="sub2api" {print $1}') || return 1
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  checksum "/proc/$pid/exe"
}
ready() {
  local result
  [[ "$(docker inspect -f '{{.State.Health.Status}}' "$container")" == healthy ]] || return 1
  result=$(curl --noproxy '*' -fsS --max-time 8 http://127.0.0.1:8080/health/ready) || return 1
  jq -e --arg id "$id" --arg role "$role" '.instance_id == $id and .role == $role and .status == "ready" and .checks.database == "ok" and .checks.redis == "ok"' <<<"$result" >/dev/null
}
verify() {
  local program=$1 readme_hash=$2 pricing_hash=$3 frontend responses health_options
  ready || return 1
  [[ "$(checksum "$mount")" == "$program" && "$(active_hash)" == "$program" && "$(container_hash /app/sub2api)" == "$program" ]] || return 1
  [[ "$(container_hash "$resources/$readme")" == "$readme_hash" && "$(container_hash "$resources/$pricing")" == "$pricing_hash" ]] || return 1
  health_options=$(docker inspect -f '{{json .HostConfig.SecurityOpt}}' "$container") || return 1
  jq -e 'index("no-new-privileges:true") != null' <<<"$health_options" >/dev/null || return 1
  nginx -t -q || return 1
  frontend=$(curl --noproxy '*' -sS --max-time 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/admin/accounts) || return 1
  responses=$(curl --noproxy '*' -sS --max-time 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/v1/responses) || return 1
  [[ "$responses" == 401 ]] || return 1
  if [[ "$role" == control ]]; then [[ "$frontend" == 200 ]]; else [[ "$frontend" == 404 ]]; fi
}
await_verified() {
  local attempt
  for attempt in {1..40}; do
    verify "$@" && return 0
    sleep 2
  done
  return 1
}
install_resource() {
  local input=$1 relative=$2 next
  if [[ -n "$resource_mount" ]]; then
    local destination="$resource_mount/$relative"
    [[ -f "$destination" && ! -L "$destination" ]] || return 1
    next=$(mktemp "${destination%/*}/.release-next.XXXXXX") || return 1
    if ! install -m 0644 -- "$input" "$next" || ! mv -fT -- "$next" "$destination"; then
      rm -f -- "$next"
      return 1
    fi
  elif [[ "$(docker inspect -f '{{.State.Running}}' "$container")" == true ]]; then
    next="$resources/$relative.release-next-$commit"
    if ! docker cp "$input" "$container:$next" || ! docker exec -u 0 "$container" sh -c 'chmod 644 "$1" && mv -f "$1" "$2"' sh "$next" "$resources/$relative"; then
      docker exec -u 0 "$container" rm -f -- "$next" || true
      return 1
    fi
  else
    chmod 644 "$input" && docker cp "$input" "$container:$resources/$relative"
  fi
}

ready
old=$(checksum "$mount")
old_readme=$(container_hash "$resources/$readme")
old_pricing=$(container_hash "$resources/$pricing")
verify "$old" "$old_readme" "$old_pricing"
if [[ "$old" == "$expected" ]]; then
  [[ "$old_readme" == "$new_readme" && "$old_pricing" == "$new_pricing" ]]
  printf 'unchanged=true\n'
  exit
fi
candidate="$stage/binary"
[[ -f "$candidate" && ! -L "$candidate" && -x "$candidate" && "$(checksum "$candidate")" == "$expected" ]]
[[ "$(checksum "$stage/README.md")" == "$new_readme" && "$(checksum "$stage/model_prices_and_context_window.json")" == "$new_pricing" ]]
line=$("$candidate" --version 2>&1)
[[ "$line" == *"Sub2API $version (commit: $commit,"* ]]

created_binary_backup=false
created_resource_backup=false
if [[ ! -e "$backup" ]]; then
  ln -- "$mount" "$backup"
  created_binary_backup=true
fi
[[ -f "$backup" && ! -L "$backup" && "$(checksum "$backup")" == "$old" ]]
resource_hash=-
if [[ "$old_readme" != "$new_readme" || "$old_pricing" != "$new_pricing" ]]; then
  if [[ ! -e "$resource_backup" ]]; then
    docker exec "$container" tar -C /app -czf - resources > "$stage/resources-before.tgz"
    mv -fT -- "$stage/resources-before.tgz" "$resource_backup"
    created_resource_backup=true
  fi
  [[ -f "$resource_backup" && ! -L "$resource_backup" ]]
  tar -xOzf "$resource_backup" "resources/$readme" > "$stage/previous-readme"
  tar -xOzf "$resource_backup" "resources/$pricing" > "$stage/previous-pricing"
  [[ "$(checksum "$stage/previous-readme")" == "$old_readme" && "$(checksum "$stage/previous-pricing")" == "$old_pricing" ]]
  resource_hash=$old_pricing
fi

rollback() {
  if [[ ! "$backup" -ef "$mount" ]]; then
    ln -- "$backup" "$stage/restore-binary" && mv -fT -- "$stage/restore-binary" "$mount" || return 1
  fi
  if [[ "$old_readme" != "$new_readme" ]]; then install_resource "$stage/previous-readme" "$readme" || return 1; fi
  if [[ "$old_pricing" != "$new_pricing" ]]; then install_resource "$stage/previous-pricing" "$pricing" || return 1; fi
  if verify "$old" "$old_readme" "$old_pricing"; then return 0; fi
  docker restart -t 10 "$container" >/dev/null && await_verified "$old" "$old_readme" "$old_pricing"
}
restore_on_failure() {
  local rc=$?
  trap - ERR
  if rollback; then
    echo "node=$id rollback=verified" >&2
    if "$created_binary_backup"; then rm -- "$backup"; fi
    if "$created_resource_backup"; then rm -- "$resource_backup"; fi
  else
    echo "node=$id rollback=failed program_backup=$backup resource_backup=$resource_backup" >&2
  fi
  exit "$rc"
}
trap restore_on_failure ERR
if [[ "$old_readme" != "$new_readme" ]]; then install_resource "$stage/README.md" "$readme"; fi
if [[ "$old_pricing" != "$new_pricing" ]]; then install_resource "$stage/model_prices_and_context_window.json" "$pricing"; fi
mv -fT -- "$candidate" "$mount"
docker restart -t 10 "$container" >/dev/null
await_verified "$expected" "$new_readme" "$new_pricing"
trap - ERR
printf 'rollback_binary_sha256=%s\nrollback_pricing_sha256=%s\n' "$old" "$resource_hash"
docker inspect -f 'started_at={{.State.StartedAt}}' "$container"
