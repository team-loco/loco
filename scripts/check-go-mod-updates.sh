#!/usr/bin/env bash
# Reports available Go module updates (minor/patch and major) across every
# go.mod in the repo. Read-only: never modifies go.mod/go.sum. Writes a
# Markdown report to the path given as $1 (default: stdout).
#
# Major-version detection works by probing the module proxy for a "path
# suffixed with the next major version" (e.g. github.com/foo/bar -> v2 at
# github.com/foo/bar/v2), per Go's module major-version-suffix convention.
# v0.x modules are skipped for major detection since that convention doesn't
# apply to them.
set -euo pipefail

out="${1:-/dev/stdout}"
: > "$out"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mod_dirs=(. k8sapi agent controller observability-proxy api gen/go)

next_major_path() {
  # $1 = module path (may already end in /vN). Echoes candidate next-major path.
  local path="$1"
  if [[ "$path" =~ ^(.*)/v([0-9]+)$ ]]; then
    local base="${BASH_REMATCH[1]}"
    local major="${BASH_REMATCH[2]}"
    echo "${base}/v$((major + 1))"
  else
    echo "${path}/v2"
  fi
}

any_findings=0

for d in "${mod_dirs[@]}"; do
  dir="$repo_root/$d"
  [ -f "$dir/go.mod" ] || continue
  label="${d}"
  [ "$d" = "." ] && label="(root)"

  section_minor=""
  section_major=""

  pushd "$dir" >/dev/null

  # --- minor/patch: go list -u flags direct deps with a newer version in [] ---
  direct_deps=$(go mod edit -json | jq -r '.Require[]? | select(.Indirect|not) | .Path')
  while IFS= read -r line; do
    path=$(awk '{print $1}' <<<"$line")
    grep -qxF "$path" <<<"$direct_deps" || continue
    newer=$(grep -oE '\[[^]]+\]' <<<"$line" || true)
    [ -n "$newer" ] || continue
    current=$(awk '{print $2}' <<<"$line")
    section_minor+="- \`$path\` $current -> ${newer//[\[\]]/}"$'\n'
  done < <(go list -m -u all 2>/dev/null || true)

  # --- major: probe the module proxy for each direct dep's next major path ---
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    current=$(go list -m -f '{{.Version}}' "$path" 2>/dev/null || true)
    [ -n "$current" ] || continue
    [[ "$current" == v0.* ]] && continue

    candidate=$(next_major_path "$path")
    versions=$(GOFLAGS=-mod=mod GOPROXY="${GOPROXY:-https://proxy.golang.org}" \
      go list -m -versions "$candidate" 2>/dev/null || true)
    [ -n "$versions" ] || continue
    [ "$versions" = "$candidate" ] && continue # go list echoes the bare path when nothing resolved

    # skip when the only available versions are pre-releases (contain a "-",
    # e.g. v2.0.0-alpha.1) -- not worth flagging until a stable release exists
    latest=""
    for v in $(awk '{for (i=2;i<=NF;i++) print $i}' <<<"$versions"); do
      [[ "$v" == *-* ]] && continue
      latest="$v" # last non-prerelease wins; proxy lists versions oldest-first
    done
    [ -n "$latest" ] || continue

    section_major+="- \`$path\` $current -> \`$candidate\` ($latest) -- needs manual review (import path change, verify it compiles)"$'\n'
  done <<<"$direct_deps"

  popd >/dev/null

  if [ -n "$section_minor" ] || [ -n "$section_major" ]; then
    any_findings=1
    {
      echo "## $label"
      if [ -n "$section_minor" ]; then
        echo ""
        echo "**Minor/patch updates available:**"
        echo ""
        echo "$section_minor"
      fi
      if [ -n "$section_major" ]; then
        echo ""
        echo "**Major version updates available:**"
        echo ""
        echo "$section_major"
      fi
    } >> "$out"
  fi
done

if [ "$any_findings" = "0" ]; then
  echo "All Go modules are up to date (no minor/patch/major updates found)." >> "$out"
fi
