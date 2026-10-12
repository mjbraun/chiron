#!/bin/bash
# The same app on the Mac, as a Mac Catalyst build: the shelf, the reader,
# the PDF view and "Chiron" in every app's share menu. Signing needs the
# team's Mac profile, which Xcode mints on first use for an account that is
# signed in (Xcode > Settings > Accounts) and a Mac it has registered.
#
#   ./scripts/mac-run.sh                build and launch
#   ./scripts/mac-run.sh build          build only; prints the app's path
#   ./scripts/mac-run.sh test [id ...]  the unit tests on the Mac build
#                                       (an id is ChironTests/SomeTests)
#   ./scripts/mac-run.sh harness [port] launch with the debug harness on
#                                       localhost:<port> (8097 by default)
#
# Built products live in ipad-app/build-mac, apart from the Simulator's.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/ipad-app"
# The Xcode project is generated, not kept in the repo; a stale one
# leaves new files out of the bundle, so it is remade when a source is newer.
"$ROOT/scripts/gen-project.sh"

DEST='platform=macOS,variant=Mac Catalyst'
APP="$PWD/build-mac/Build/Products/Debug-maccatalyst/Chiron.app"
SIGNING=(-allowProvisioningUpdates -allowProvisioningDeviceRegistration)

build() {
  xcodebuild -project Chiron.xcodeproj -scheme Chiron -destination "$DEST" \
    -derivedDataPath build-mac "${SIGNING[@]}" build -quiet
  echo "$APP"
}

case "${1:-run}" in
  build) build ;;
  run) build >/dev/null; open -a "$APP" ;;
  harness) build >/dev/null; open -a "$APP" --args harness "harness_port=${2:-8097}" ;;
  test)
    shift
    only=()
    for id in "$@"; do only+=(-only-testing:"$id"); done
    [ ${#only[@]} -eq 0 ] && only=(-only-testing:ChironTests)
    xcodebuild test -project Chiron.xcodeproj -scheme Chiron -destination "$DEST" \
      -derivedDataPath build-mac "${SIGNING[@]}" "${only[@]}" | grep -E "^(Test (Case|Suite)|\s+Executed|.*error:|\*\* TEST)" ;;
  *) echo "usage: $0 [build|run|test [id ...]|harness [port]]" >&2; exit 1 ;;
esac
