#!/bin/bash
# What each line of `pmset -g pslog` does to the lid. The daemon itself needs
# root and a power cable to exercise; this is the part that decides.
set -uo pipefail
cd "$(dirname "$0")"
MAC_AWAKE_LIB=1 . ./mac-awake

fail=0
check() { # <what> <expected> <line>
  got=$(lid_setting "$3")
  if [ "$got" = "$2" ]; then echo "ok   $1"; else echo "FAIL $1: wanted '$2', got '$got'"; fail=1; fi
}

check "plugged in keeps the lid from sleeping it" 1 "Now drawing from 'AC Power'"
check "on battery the lid sleeps it"              0 "Now drawing from 'Battery Power'"
check "a UPS counts as battery"                   0 "Now drawing from 'UPS Power'"
check "the battery detail line decides nothing"   "" " -InternalBattery-0 (id=7602275)	100%; charged; 0:00 remaining present: true"
check "a blank line decides nothing"              "" ""
check "an unfamiliar line decides nothing"        "" "Now drawing from 'Something New'"

# Only a change from the charger to battery is news; a daemon that starts
# on battery, or a second battery line, says nothing.
alerting() { # <what> <expected> <was> <now>
  got=$(alerts_on "$3" "$4")
  if [ "$got" = "$2" ]; then echo "ok   $1"; else echo "FAIL $1: wanted '$2', got '$got'"; fail=1; fi
}
alerting "unplugged is news"                 1 1 0
alerting "starting on battery is not"        "" "" 0
alerting "battery again is not"              "" 0 0
alerting "plugged back in is not"            "" 0 1

# The alert goes to the book with the key on curl's stdin, never in its
# arguments, where any user's ps would see it.
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
printf 'url=https://book.example\nkey=not-a-real-key\nname=Truman\n' > "$T/conf"
curl() { printf '%s\n' "$@" > "$T/args"; cat > "$T/stdin"; }
ALERT_CONF="$T/conf" alert "Truman is on battery"
if grep -q "https://book.example/alerts" "$T/args" && grep -q '"text":"Truman is on battery"' "$T/args" \
   && grep -q "not-a-real-key" "$T/stdin" && ! grep -q "not-a-real-key" "$T/args"; then
  echo "ok   the alert is posted with the key kept off the command line"
else
  echo "FAIL the alert: args $(tr '\n' ' ' < "$T/args") stdin $(cat "$T/stdin")"; fail=1
fi
rm -f "$T/args"
ALERT_CONF="$T/none" alert "nothing configured"
[ ! -e "$T/args" ] && echo "ok   with no config there is no alert" || { echo "FAIL alerted with no config"; fail=1; }
unset -f curl

[ $fail = 0 ] && echo "all good"
exit $fail
