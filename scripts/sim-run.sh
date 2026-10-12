#!/bin/bash
# Build the app, install it in a simulator, and launch it pointed at a dev
# server. The CHIRON_SERVER launch-environment override means no saved-server
# state is involved (the simulator shares the Mac's loopback).
#
#   ./scripts/sim-run.sh [app args...]      build + install + launch
#   ./scripts/sim-run.sh selftest [args]    launch the self-test, wait for its
#                                           verdict, exit 0 on PASS
#                                           (args: level=1..5 subject=<id> selftestweak)
#   ./scripts/sim-run.sh test               run every test (unit and UI)
#   ./scripts/sim-run.sh parallel [workers] [id ...]
#                                           run tests across simulator
#                                           clones (2 by default), each
#                                           worker on its own dev server.
#                                           Not the default: at this suite's
#                                           size the clones cost about what
#                                           they save, and two simulators
#                                           contending drop touches. Here
#                                           for when the suite is longer.
#   ./scripts/sim-run.sh fast [id ...]      build once, then run only the
#                                           given tests against that build
#                                           (an id is ChironTests/SomeTests
#                                           or .../someTest, repeatable; with
#                                           no id, the unit target alone).
#                                           Reuses the built products, so a
#                                           red-green loop pays the build once.
#   ./scripts/sim-run.sh shot [name]        screenshot to /tmp/<name>.png
#
# DEVICE picks the simulator by name: chiron-ipad (iPad A16, current iPadOS)
# or chiron-ipad-15 (iPad mini 4 shape, iOS 15.5 - the gate for the real
# device). CHIRON_SERVER picks the server; never point it at :8080 (real
# learner state). The default :8082 is the isolated sim server.
set -euo pipefail

DEVICE="${DEVICE:-chiron-ipad}"
BUNDLE=dev.mjbraun.chiron
SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
APPDIR="$SCRIPTS/../ipad-app"
SERVER="${CHIRON_SERVER:-http://localhost:8082}"

# The Xcode project is generated, not kept in the repo; a stale one
# leaves new files out of the bundle, so it is remade when a source is newer.
"$SCRIPTS/gen-project.sh"

UDID=$(xcrun simctl list devices | grep -E "^\s+$DEVICE \(" | head -1 | sed -E 's/.*\(([0-9A-F-]{36})\).*/\1/')
if [ -z "$UDID" ]; then
  echo "no simulator named $DEVICE (xcrun simctl list devices)" >&2
  exit 1
fi

cmd="${1:-}"
case "$cmd" in
  shot)
    out="/tmp/${2:-sim}.png"
    xcrun simctl io "$UDID" screenshot "$out" >/dev/null 2>&1
    echo "$out"
    exit 0 ;;
  parallel)
    shift || true
    workers="${1:-2}"
    case "$workers" in ''|*[!0-9]*) workers=2 ;; *) shift || true ;; esac
    # One dev server per worker, each with its own state: two apps marking
    # up the same chapter on one server is the conflict the reader sees
    # when two devices do it, and it would land mid-test.
    for i in $(seq 0 $((workers - 1))); do
      "$SCRIPTS/sim-server.sh" start $((8084 + i)) >/dev/null
    done
    # An app left running in any booted simulator holds a harness port,
    # and every simulator shares the Mac's loopback: a worker would read
    # the stranger's state.
    for booted in $(xcrun simctl list devices booted | sed -nE 's/.*\(([0-9A-F-]{36})\).*/\1/p'); do
      xcrun simctl terminate "$booted" "$BUNDLE" 2>/dev/null || true
    done
    xcrun simctl bootstatus "$UDID" -b >/dev/null
    defaults write com.apple.iphonesimulator ConnectHardwareKeyboard -bool false
    cd "$APPDIR"
    xcodebuild -project Chiron.xcodeproj -scheme Chiron -skipPackagePluginValidation \
      -destination "platform=iOS Simulator,id=$UDID" \
      -derivedDataPath "build-sim" build-for-testing -quiet 2>&1 | grep -vE "^$|objc\[[0-9]+\]: Class" || true
    run=$(ls -t build-sim/Build/Products/Chiron_iphonesimulator*.xctestrun 2>/dev/null | head -1)
    [ -n "$run" ] || { echo "no xctestrun; build-for-testing failed" >&2; exit 1; }
    only=()
    for id in "$@"; do only+=("-only-testing:$id"); done
    log="build-sim/parallel.xcresult"
    rm -rf "$log"
    xcodebuild test-without-building -xctestrun "$run" -resultBundlePath "$log" \
      -destination "platform=iOS Simulator,id=$UDID" "${only[@]}" \
      -parallel-testing-enabled YES -maximum-parallel-testing-workers "$workers" -quiet 2>&1 \
      | grep -vE "^$|objc\[[0-9]+\]: Class" || true
    [ -d "$log" ] || { echo "no test result bundle" >&2; exit 1; }
    xcrun xcresulttool get test-results summary --path "$log" 2>/dev/null | grep -E '"(result|totalTestCount|passedTests|failedTests)"' || true
    xcrun xcresulttool get test-results summary --path "$log" 2>/dev/null | grep -q '"result" : "Passed"'
    exit $? ;;
  fast)
    shift || true
    xcrun simctl bootstatus "$UDID" -b >/dev/null
    defaults write com.apple.iphonesimulator ConnectHardwareKeyboard -bool false
    cd "$APPDIR"
    xcodebuild -project Chiron.xcodeproj -scheme Chiron -skipPackagePluginValidation \
      -destination "platform=iOS Simulator,id=$UDID" \
      -derivedDataPath "build-sim" build-for-testing -quiet 2>&1 | grep -vE "^$|objc\[[0-9]+\]: Class" || true
    # The scheme's own .xctestrun is the one that carries both test targets;
    # the project-named one has only the unit tests.
    run=$(ls -t build-sim/Build/Products/Chiron_iphonesimulator*.xctestrun 2>/dev/null | head -1)
    [ -n "$run" ] || { echo "no xctestrun; build-for-testing failed" >&2; exit 1; }
    only=()
    if [ "$#" -eq 0 ]; then
      only=(-only-testing:ChironTests)
    else
      for id in "$@"; do only+=("-only-testing:$id"); done
    fi
    # test-without-building writes no bundle into the scheme's log dir, so
    # it is told where to put one; reading the newest there would report the
    # last full run's verdict instead of this one's.
    log="build-sim/fast.xcresult"
    rm -rf "$log"
    xcodebuild test-without-building -xctestrun "$run" -resultBundlePath "$log" \
      -destination "platform=iOS Simulator,id=$UDID" "${only[@]}" -quiet 2>&1 \
      | grep -vE "^$|objc\[[0-9]+\]: Class" || true
    [ -d "$log" ] || { echo "no test result bundle" >&2; exit 1; }
    xcrun xcresulttool get test-results summary --path "$log" 2>/dev/null | grep -E '"(result|totalTestCount|passedTests|failedTests)"' || true
    xcrun xcresulttool get test-results summary --path "$log" 2>/dev/null | grep -q '"result" : "Passed"'
    exit $? ;;
  test)
    # The Mac's keyboard stands in for the iPad's while it is connected,
    # and a test about the software keyboard would never see it.
    defaults write com.apple.iphonesimulator ConnectHardwareKeyboard -bool false
    xcrun simctl bootstatus "$UDID" -b >/dev/null
    # The test host is reinstalled by xcodebuild; on iOS 15.5 that install
    # fails over an existing copy (see below), so clear it first.
    xcrun simctl uninstall "$UDID" "$BUNDLE" 2>/dev/null || true
    cd "$APPDIR"
    xcodebuild -project Chiron.xcodeproj -scheme Chiron -skipPackagePluginValidation \
      -destination "platform=iOS Simulator,id=$UDID" \
      -derivedDataPath "build-sim" test -quiet 2>&1 | grep -vE "^$|objc\[[0-9]+\]: Class" || true
    # xcodebuild's own exit status is lost in the pipe; the result bundle says.
    log=$(ls -td build-sim/Logs/Test/*.xcresult 2>/dev/null | head -1)
    [ -n "$log" ] || { echo "no test result bundle" >&2; exit 1; }
    xcrun xcresulttool get test-results summary --path "$log" 2>/dev/null | grep -E '"(result|totalTestCount|passedTests|failedTests)"' || true
    xcrun xcresulttool get test-results summary --path "$log" 2>/dev/null | grep -q '"result" : "Passed"'
    exit $? ;;
esac

if [ "$SERVER" = "http://localhost:8082" ]; then
  "$SCRIPTS/sim-server.sh" start
fi

xcrun simctl bootstatus "$UDID" -b >/dev/null
# Xcode 27 ships no Simulator.app to open; the device runs headless then.
open -a Simulator 2>/dev/null || true

cd "$APPDIR"
xcodebuild -project Chiron.xcodeproj -scheme Chiron -skipPackagePluginValidation \
  -destination "platform=iOS Simulator,id=$UDID" \
  -derivedDataPath build-sim build -quiet

xcrun simctl terminate "$UDID" "$BUNDLE" 2>/dev/null || true
APP=build-sim/Build/Products/Debug-iphonesimulator/Chiron.app
if ! xcrun simctl install "$UDID" "$APP" 2>/dev/null; then
  # The iOS 15.5 runtime cannot delta-install over a copy that is already
  # there ("Could not hardlink copy"); a clean install works. The app's
  # Documents go with it, so on that device every run starts from nothing.
  xcrun simctl uninstall "$UDID" "$BUNDLE" 2>/dev/null || true
  xcrun simctl install "$UDID" "$APP"
fi

if [ "$cmd" = "selftest" ]; then
  log="/tmp/chiron-selftest-$DEVICE.log"
  : > "$log"
  SIMCTL_CHILD_CHIRON_SERVER="$SERVER" xcrun simctl launch --console-pty "$UDID" "$BUNDLE" "$@" >"$log" 2>&1 &
  pid=$!
  for _ in $(seq 1 "${SELFTEST_TIMEOUT:-600}"); do
    grep -qE "chiron-selftest (PASS|FAIL)" "$log" && break
    sleep 1
  done
  kill "$pid" 2>/dev/null || true
  xcrun simctl terminate "$UDID" "$BUNDLE" 2>/dev/null || true
  grep "chiron-selftest" "$log" || { echo "no self-test output in $log" >&2; exit 1; }
  grep -q "chiron-selftest PASS" "$log"
  exit $?
fi

SIMCTL_CHILD_CHIRON_SERVER="$SERVER" xcrun simctl launch "$UDID" "$BUNDLE" "$@"
