#!/bin/bash
# The app built and signed on a Mac with nobody at it: the unit suite in
# the Simulator, then a development-signed IPA for the registered
# devices, the Catalyst app as a zip, and the manifest an iPad installs
# from. Run by the runner's `build` verb on the MacBook; runs on any Mac
# with Xcode, the repo, and ~/.config/chiron-runner/config:
#
#   team_id = <your Apple team id>
#   builds_url = https://<your-sprite>.sprites.app/builds
#   asc_key_id = ABC123          # App Store Connect API key, for signing
#   asc_issuer_id = <uuid>       # with no Apple ID session to expire;
#   asc_key_path = ~/.private_keys/AuthKey_ABC123.p8
#   ui_tests = 0                 # 1 runs the UI walk too (minutes)
#
# Without the three asc_ lines, Xcode's signed-in account signs instead.
#
#   scripts/mac-build.sh <ref>     builds ~/builds/<id>/, prints the id last
#
# The build carries CFBundleVersion = the commit count and
# CFBundleShortVersionString = the date, so every build is ordered and
# named. Everything an installer needs is in the build directory:
# manifest.plist, Chiron.ipa, Chiron-mac.zip, build.json, build.log.
set -euo pipefail
# A forced-command ssh session carries the system PATH alone; Homebrew
# (xcodegen) lives beside it.
export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"
REF="${1:?a ref to build}"
cd "$(dirname "$0")/.."
ROOT=$PWD
CONF="$HOME/.config/chiron-runner/config"
conf() { [ -f "$CONF" ] && sed -n "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*//p" "$CONF" | head -1 | sed "s|^~|$HOME|" || true; }
TEAM=$(conf team_id); TEAM=${TEAM:-${CHIRON_TEAM_ID:-}}
[ -n "$TEAM" ] || { echo "set team_id in ~/.config/chiron-runner/config (or CHIRON_TEAM_ID)" >&2; exit 1; }
# project.yml takes the team from here when xcodegen generates it.
export CHIRON_TEAM_ID="$TEAM"
BUILDS_URL=$(conf builds_url); BUILDS_URL=${BUILDS_URL:-${CHIRON_BUILDS_URL:-}}
[ -n "$BUILDS_URL" ] || { echo "set builds_url in ~/.config/chiron-runner/config (or CHIRON_BUILDS_URL)" >&2; exit 1; }
AUTH=()
if [ -n "$(conf asc_key_id)" ]; then
  AUTH=(-authenticationKeyPath "$(conf asc_key_path)" -authenticationKeyID "$(conf asc_key_id)" -authenticationKeyIssuerID "$(conf asc_issuer_id)")
fi

git fetch -q . 2>/dev/null || true
git checkout -q --detach "$REF"
trap 'cd "$ROOT" && git checkout -q main 2>/dev/null || true' EXIT
COMMIT=$(git rev-parse --short HEAD)
COUNT=$(git rev-list --count HEAD)
SHORT=$(date -u +%Y.%-m.%-d)
ID="$(date -u +%Y%m%d-%H%M%S)-$COMMIT"
TOKEN=$(openssl rand -hex 32)
OUT="$HOME/builds/$ID"
mkdir -p "$OUT"
LOG="$OUT/build.log"
STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "==> build $ID of $REF ($COMMIT, #$COUNT) into $OUT" | tee "$LOG"

step() { echo "==> $*" | tee -a "$LOG"; }
fail() { echo "==> FAILED: $*" | tee -a "$LOG"; finish failed "$*"; exit 1; }
finish() {
  python3 - "$OUT/build.json" "$ID" "$TOKEN" "$COMMIT" "$COUNT" "$SHORT" "$STARTED" "$1" "${2:-}" "${TESTS_PASSED:-0}" "${TESTS_FAILED:-0}" "$REF" <<'PY'
import json, sys, datetime
_, path, id_, token, commit, count, short, started, status, reason, passed, failed, ref = sys.argv
json.dump({"id": id_, "token": token, "ref": ref, "commit": commit, "version": short, "build": int(count),
           "status": status, "reason": reason, "tests": {"passed": int(passed), "failed": int(failed)},
           "started": started, "finished": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")},
          open(path, "w"), indent=2)
PY
}

cd ipad-app
step "xcodegen"
xcodegen generate -q >>"$LOG" 2>&1 || fail "xcodegen"

step "unit tests in the Simulator"
if ! ../scripts/sim-run.sh fast ChironTests >>"$LOG" 2>&1; then
  fail "unit tests (see build.log)"
fi
read -r TESTS_PASSED TESTS_FAILED < <(xcrun xcresulttool get test-results summary --path build-sim/fast.xcresult 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("passedTests",0), d.get("failedTests",0))')
step "tests: $TESTS_PASSED passed, $TESTS_FAILED failed"
[ "$TESTS_FAILED" -eq 0 ] || fail "$TESTS_FAILED unit tests failed"
if [ "$(conf ui_tests)" = "1" ]; then
  step "UI tests in the Simulator"
  ../scripts/sim-run.sh test >>"$LOG" 2>&1 || fail "UI tests (see build.log)"
fi

# Debug, as every build on the devices has been: the harness and the
# agent link the sprite drives the app through live behind #if DEBUG.
step "archive for iOS"
xcodebuild -project Chiron.xcodeproj -scheme Chiron -skipPackagePluginValidation -configuration Debug -destination 'generic/platform=iOS' \
  -archivePath "$OUT/Chiron.xcarchive" -allowProvisioningUpdates ${AUTH[@]+"${AUTH[@]}"} \
  CURRENT_PROJECT_VERSION="$COUNT" MARKETING_VERSION="$SHORT" archive >>"$LOG" 2>&1 || fail "archive"

# SwiftTerm's build plugin wants a person's approval once per Mac;
# nobody is at this one, so validation is skipped as sim-run.sh does.
step "export a development-signed IPA"
cat > "$OUT/export.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>development</string>
  <key>teamID</key><string>$TEAM</string>
  <key>signingStyle</key><string>automatic</string>
  <key>destination</key><string>export</string>
  <key>thinning</key><string>&lt;none&gt;</string>
  <key>compileBitcode</key><false/>
</dict></plist>
PLIST
xcodebuild -exportArchive -archivePath "$OUT/Chiron.xcarchive" -exportOptionsPlist "$OUT/export.plist" \
  -exportPath "$OUT/export" -allowProvisioningUpdates ${AUTH[@]+"${AUTH[@]}"} >>"$LOG" 2>&1 || fail "export"
mv "$OUT/export/Chiron.ipa" "$OUT/Chiron.ipa"

step "the Mac app"
xcodebuild -project Chiron.xcodeproj -scheme Chiron -skipPackagePluginValidation -destination 'platform=macOS,variant=Mac Catalyst' \
  -derivedDataPath build-mac -allowProvisioningUpdates ${AUTH[@]+"${AUTH[@]}"} \
  CURRENT_PROJECT_VERSION="$COUNT" MARKETING_VERSION="$SHORT" build >>"$LOG" 2>&1 || fail "mac build"
ditto -c -k --keepParent build-mac/Build/Products/Debug-maccatalyst/Chiron.app "$OUT/Chiron-mac.zip"

step "manifest"
cat > "$OUT/manifest.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>items</key><array><dict>
    <key>assets</key><array><dict>
      <key>kind</key><string>software-package</string>
      <key>url</key><string>$BUILDS_URL/$TOKEN/Chiron.ipa</string>
    </dict></array>
    <key>metadata</key><dict>
      <key>bundle-identifier</key><string>dev.mjbraun.chiron</string>
      <key>bundle-version</key><string>$SHORT</string>
      <key>kind</key><string>software</string>
      <key>title</key><string>Chiron $SHORT ($COUNT)</string>
    </dict>
  </dict></array>
</dict></plist>
PLIST

rm -rf "$OUT/Chiron.xcarchive" "$OUT/export" "$OUT/export.plist"
finish ready
step "done: $(du -sh "$OUT/Chiron.ipa" | cut -f1) IPA, $(du -sh "$OUT/Chiron-mac.zip" | cut -f1) Mac zip"
echo "$ID"
