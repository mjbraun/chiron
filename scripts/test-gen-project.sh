#!/bin/bash
# gen-project.sh makes the Xcode project from project.yml. A project left
# over from before a file was added leaves that file out of the bundle, so
# the project is made again whenever a source is newer than it, and the
# team the last generation had stays when the environment sets none.
# Needs xcodegen (a Mac with Homebrew).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/repo/scripts" "$T/repo/ipad-app/Probe"
cp "$HERE/gen-project.sh" "$T/repo/scripts/"
cat > "$T/repo/ipad-app/project.yml" <<'YML'
name: Probe
targets:
  Probe:
    type: tool
    platform: macOS
    sources: [Probe]
    settings:
      DEVELOPMENT_TEAM: ${CHIRON_TEAM_ID}
YML
echo 'print("one")' > "$T/repo/ipad-app/Probe/One.swift"
PBX="$T/repo/ipad-app/Probe.xcodeproj/project.pbxproj"
passed=0
fails=()
check() { if eval "$2"; then passed=$((passed + 1)); else fails+=("$1"); fi; }

CHIRON_TEAM_ID=TEAM123456 "$T/repo/scripts/gen-project.sh" Probe
check "the first run makes the project" '[ -f "$PBX" ]'
check "the team reaches the project" 'grep -q "DEVELOPMENT_TEAM = TEAM123456" "$PBX"'

sleep 1
echo 'print("two")' > "$T/repo/ipad-app/Probe/Two.swift"
unset CHIRON_TEAM_ID
"$T/repo/scripts/gen-project.sh" Probe
check "a file added since is in the project" 'grep -q "Two.swift" "$PBX"'
check "the team stays when the environment sets none" 'grep -q "DEVELOPMENT_TEAM = TEAM123456" "$PBX"'

before=$(stat -f %m "$PBX")
sleep 1
"$T/repo/scripts/gen-project.sh" Probe
check "nothing newer leaves the project alone" '[ "$(stat -f %m "$PBX")" = "$before" ]'

if [ ${#fails[@]} -eq 0 ]; then
  echo "gen-project: $passed passed"
else
  printf 'FAIL: %s\n' "${fails[@]}" >&2
  exit 1
fi
