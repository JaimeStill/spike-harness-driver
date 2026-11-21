#!/usr/bin/env bash
# Prints `<file>: <tool> <pin> -> <latest>` for each mise.toml and go.mod pin behind its latest, and
# exits 1 if any; prints nothing when all are current. Compares mise.toml's tools, a newer marathon
# tag included, with their latest. A pin held on purpose, as Pi's and OpenCode's are at the versions
# clutch's conformance suite measured, carries a comment beside it saying why; a trailing held pin
# is reported like any other, marked `(held)`.
# Compares each go.work module's direct requirements in its go.mod with their latest on the module
# proxy, in workspace mode, skipping the workspace's own modules. Reads the network.
# See docs/configuration.md#currency
set -euo pipefail
cd "$(dirname "$0")/.."

trailing=

report() {
  trailing+=${trailing:+$'\n'}$1
}

report_mise_tools() {
  local outdated path tool pin latest line
  outdated=$(MISE_LOG_LEVEL=error mise outdated --bump --local --json |
    jq -r 'to_entries[] | (.value.bump // .value.latest) as $to | select($to != .value.requested) |
      [.value.source.path, .key, .value.requested, $to] | @tsv' | sort -u) || return
  while IFS=$'\t' read -r path tool pin latest; do
    [[ -n $path ]] || continue
    line="${path##*/}: $tool $pin -> $latest"
    held "$path" "$tool" && line+=" (held)"
    report "$line"
  done <<<"$outdated"
}

held() {
  awk -v tool="$2" '
    /^[[:space:]]*\[/ { tools = ($0 ~ /^[[:space:]]*\[tools\][[:space:]]*(#.*)?$/); next }
    tools {
      key = $0
      sub(/[[:space:]]*=.*/, "", key)
      gsub(/^[[:space:]"]+|"$/, "", key)
      if (key == tool && $0 ~ /#/) found = 1
    }
    END { exit !found }' "$1"
}

report_go_requirements() {
  local workspace mods mod gomod requires listed path pin selected update
  local -A pins
  workspace=$(go list -m) || return
  mods=$(go work edit -json | jq -r '.Use[].DiskPath') || return
  for mod in $mods; do
    gomod=${mod#./}/go.mod
    [[ $mod == . ]] && gomod=go.mod
    requires=$(cd "$mod" && go mod edit -json | jq -r --arg workspace "$workspace" '
      ($workspace | split("\n")) as $own | .Require // [] | .[] |
      select((.Indirect | not) and (.Path | IN($own[]) | not)) | "\(.Path) \(.Version)"') || return
    [[ -n $requires ]] || continue
    pins=()
    while read -r path pin; do
      pins[$path]=$pin
    done <<<"$requires"
    listed=$(cd "$mod" && go list -m -u -f '{{.Path}} {{.Version}} {{with .Update}}{{.Version}}{{end}}' "${!pins[@]}") || return
    while read -r path selected update; do
      [[ ${pins[$path]} == "${update:-$selected}" ]] || report "$gomod: $path ${pins[$path]} -> ${update:-$selected}"
    done <<<"$listed"
  done
}

report_mise_tools
report_go_requirements

if [[ -n $trailing ]]; then
  echo "$trailing"
  exit 1
fi
