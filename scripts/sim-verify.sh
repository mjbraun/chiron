#!/bin/bash
# Walk every screen of the iPad app in the Simulator and screenshot each,
# driven through the app's debug harness (launch argument `harness`, HTTP on
# localhost:8087). The harness output is asserted; the screenshots are for
# review by eye.
#
#   ./scripts/sim-verify.sh                 both devices, light and dark, both text sizes
#   DEVICES="chiron-ipad-15" THEMES=light SIZES=large ./scripts/sim-verify.sh
#   SUBJECT=data ./scripts/sim-verify.sh    walk the other book
#
# Output: $OUT/<device>/<theme>-<size>/<nn>-<screen>.png (default
# ipad-app/verify/, gitignored). Needs a throwaway server with both subjects
# on CHIRON_SERVER (default :8084); never point it at :8080.
set -euo pipefail

SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
ROOT="$SCRIPTS/.."
BUNDLE=dev.mjbraun.chiron
SERVER="${CHIRON_SERVER:-http://localhost:8084}"
SUBJECT="${SUBJECT:-ai}"
LEVEL="${LEVEL:-2}"
OUT="${OUT:-$ROOT/ipad-app/verify}"
# The iOS 15 device is retired; chiron-ipad (current iPadOS) is the loop.
DEVICES="${DEVICES:-chiron-ipad}"
THEMES="${THEMES:-light dark}"
# "large" is the system default size; "default" is not a name simctl knows.
SIZES="${SIZES:-large accessibility-extra-large}"
PORT=8087
H="http://localhost:$PORT"

# The server must be fresh for the subject so the walk starts at placement.
reset_subject() {
  curl -sf -o /dev/null -X POST -H 'Content-Type: application/json' \
    -d "{\"subject\":\"$SUBJECT\",\"confirm\":true}" "$SERVER/reset"
}

udid_of() {
  xcrun simctl list devices | grep -E "^\s+$1 \(" | head -1 | sed -E 's/.*\(([0-9A-F-]{36})\).*/\1/'
}

# state: print the harness state, one line.
state() { curl -sf "$H/state"; echo; }

# expect <screen>: assert the harness reports that screen.
expect() {
  local got
  got=$(curl -sf "$H/state" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("screen",""))')
  if [ "$got" != "$1" ]; then
    echo "expected screen $1, got $got" >&2
    curl -sf "$H/state" >&2; echo >&2
    exit 1
  fi
}

# cmd <path> [json]: a harness POST.
cmd() {
  curl -sf -X POST -H 'Content-Type: application/json' -d "${2:-{\}}" "$H$1" >/dev/null
}

wait_for_harness() {
  for _ in $(seq 1 60); do
    curl -sf "$H/state" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  echo "harness never came up on $PORT" >&2
  exit 1
}

# settle: SwiftUI and the web views need a beat after a state change before
# a screenshot shows the new screen.
settle() { sleep "${1:-1.5}"; }

shot() {
  settle "${3:-1.5}"
  xcrun simctl io "$UDID" screenshot "$DIR/$1-$2.png" >/dev/null 2>&1
  echo "  $1-$2.png  $(state)"
}

for DEVICE in $DEVICES; do
  UDID=$(udid_of "$DEVICE")
  [ -n "$UDID" ] || { echo "no simulator named $DEVICE" >&2; exit 1; }
  echo "== $DEVICE ($UDID)"
  xcrun simctl bootstatus "$UDID" -b >/dev/null
  (cd "$ROOT/ipad-app" && xcodebuild -project Chiron.xcodeproj -scheme Chiron -skipPackagePluginValidation \
    -destination "platform=iOS Simulator,id=$UDID" -derivedDataPath build-sim build -quiet 2>&1 \
    | grep -E "error:" || true)
  APP="$ROOT/ipad-app/build-sim/Build/Products/Debug-iphonesimulator/Chiron.app"
  xcrun simctl terminate "$UDID" "$BUNDLE" 2>/dev/null || true
  if ! xcrun simctl install "$UDID" "$APP" 2>/dev/null; then
    xcrun simctl uninstall "$UDID" "$BUNDLE" 2>/dev/null || true
    xcrun simctl install "$UDID" "$APP"
  fi

  for THEME in $THEMES; do
    for SIZE in $SIZES; do
      DIR="$OUT/$DEVICE/$THEME-$SIZE"
      mkdir -p "$DIR"
      rm -f "$DIR"/*.png
      echo "-- $THEME $SIZE -> $DIR"
      xcrun simctl ui "$UDID" appearance "$THEME"
      xcrun simctl ui "$UDID" content_size "$SIZE"
      reset_subject
      xcrun simctl terminate "$UDID" "$BUNDLE" 2>/dev/null || true
      SIMCTL_CHILD_CHIRON_SERVER="$SERVER" SIMCTL_CHILD_CHIRON_SHELL_USER="$USER" \
        xcrun simctl launch "$UDID" "$BUNDLE" harness "harness_port=$PORT" >/dev/null
      wait_for_harness
      # The app opens on the shelf.
      expect bookshelf
      # A capture: the card as the share sheet opens it, then a primer on
      # the shelf (a dev server writes a stub at once), read with no check,
      # and grown by a margin note.
      cmd /capture/card '{"text":"User-agent: *\nContent-Signal: search=yes, ai-input=no","url":"https://lexweekly.example/robots.txt","app":"Safari"}'
      shot 00a capture-card 1.5
      cmd /capture/close
      primer=$(curl -sf -X POST -H 'Content-Type: application/json' \
        -d '{"text":"User-agent: *\nContent-Signal: search=yes, ai-input=no","url":"https://lexweekly.example/robots.txt","app":"Safari","prompt":"How does a crawler read Content-Signal?"}' \
        "$H/capture" | python3 -c 'import json,sys; print(json.load(sys.stdin)["subject"])')
      [ -n "$primer" ] || { echo "capture returned no subject" >&2; exit 1; }
      shot 00b shelf-with-primer 1.5
      cmd /open "{\"subject\":\"$primer\"}"
      expect reading
      shot 00c primer 2
      cmd /tool '{"tool":"note"}'
      cmd /note '{"text":"ai-input=no","note":"Who actually honours this?"}'
      shot 00d primer-extended 2.5
      cmd /close
      cmd /tool '{"tool":"none"}'
      cmd /shelf
      expect bookshelf
      shot 01 bookshelf
      cmd /open "{\"subject\":\"$SUBJECT\"}"
      expect placement
      shot 02 placement
      cmd /place "{\"level\":$LEVEL}"
      expect series
      shot 03 series
      cmd /answer '{"mode":"correct"}'
      expect results
      shot 04 results-calibration 2.5
      cmd /proceed
      expect reading
      shot 05 reading 3
      cmd /contents
      shot 05b contents 1.5
      cmd /contents
      cmd /chrome
      shot 05c reading-chrome-hidden 1
      cmd /chrome
      # The reader's marks: a highlight, then a question on a passage, asked
      # (a dev server answers with a stub), then the card closed.
      cmd /tool '{"tool":"highlighter"}'
      cmd /mark '{"text":"predict the next token","kind":"highlight"}'
      shot 05d highlight 1.5
      cmd /tool '{"tool":"ask"}'
      cmd /ask '{"text":"at sufficient scale","question":"What counts as sufficient scale?"}'
      shot 05e ask-answered 2
      cmd /close
      cmd /tool '{"tool":"none"}'
      # The shell, when a gate is up: CHIRON_GATE=http://localhost:8090
      # CHIRON_GATE_KEY=k1 with a user-mode sshd behind it that accepts the
      # key the app enrols (the server's CHIRON_AUTHORIZED_KEYS must be the
      # file that sshd reads). The app signs in as $USER on the Mac.
      if [ -n "${CHIRON_GATE:-}" ]; then
        cmd /server "{\"url\":\"$CHIRON_GATE\",\"key\":\"${CHIRON_GATE_KEY:-}\",\"name\":\"gate\"}"
        cmd /shell
        for _ in $(seq 1 40); do
          phase=$(curl -sf "$H/shell/screen" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("phase",""))')
          [ "$phase" = "connected" ] && break
          case "$phase" in closed*) echo "shell failed: $phase" >&2; exit 1;; esac
          sleep 0.5
        done
        [ "$phase" = "connected" ] || { echo "shell never connected: $phase" >&2; exit 1; }
        cmd /shell/type '{"text":"echo shell-$((6*7))\n"}'
        settle 1.5
        curl -sf "$H/shell/screen" | grep -q 'shell-42' || { echo "typed command did not echo back" >&2; curl -sf "$H/shell/screen" >&2; exit 1; }
        shot 05f shell 1
        cmd /shell/close
      else
        echo "  (no CHIRON_GATE; shell step skipped)"
      fi
      cmd /check
      expect check
      shot 06 check
      # One inked answer (a stub transcription on a dev server), one pass,
      # the rest typed and chosen: the results must show READ AS for all.
      cmd /answer '{"mode":"mixed"}'
      expect results
      shot 07 results-mixed 2.5
      # Break time is off by default; on, a long chunk earns a break
      # suggestion and a short one goes straight on.
      cmd /breaks
      cmd /proceed
      screen=$(curl -sf "$H/state" | python3 -c 'import json,sys; print(json.load(sys.stdin)["screen"])')
      if [ "$screen" = "break" ]; then
        shot 08 break
        cmd /proceed
      fi
      expect reading
      shot 09 remediation 3
      xcrun simctl terminate "$UDID" "$BUNDLE" 2>/dev/null || true
    done
  done
  xcrun simctl ui "$UDID" appearance light
  xcrun simctl ui "$UDID" content_size large
done
echo "screenshots in $OUT"
