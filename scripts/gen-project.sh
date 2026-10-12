#!/bin/bash
# The Xcode project is generated from project.yml, never kept in the repo.
# A project from before a source file was added leaves that file out of
# the bundle, and a build from it looks fine until the page cannot find
# the script. So the project is made again whenever project.yml or any
# source is newer than it. The team id comes from CHIRON_TEAM_ID, or from
# the project already there when the environment sets none.
#
#   scripts/gen-project.sh [name]     name is the project's, Chiron by default
set -euo pipefail
cd "$(dirname "$0")/../ipad-app"
NAME="${1:-Chiron}"
PBX="$NAME.xcodeproj/project.pbxproj"

if [ -z "${CHIRON_TEAM_ID:-}" ] && [ -f "$PBX" ]; then
  CHIRON_TEAM_ID=$(sed -nE 's/.*DEVELOPMENT_TEAM = ([A-Z0-9]+);.*/\1/p' "$PBX" | head -1)
fi
export CHIRON_TEAM_ID="${CHIRON_TEAM_ID:-}"

sources=(project.yml)
for d in */; do
  case "$d" in build*|*.xcodeproj/|fixtures/|verify/) ;; *) sources+=("$d") ;; esac
done
if [ ! -f "$PBX" ] || [ -n "$(find "${sources[@]}" -newer "$PBX" -print -quit)" ]; then
  xcodegen generate -q
fi
