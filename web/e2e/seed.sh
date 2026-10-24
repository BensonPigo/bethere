#!/usr/bin/env bash
# Create test events for the end-to-end routes (docs/process/e2e-test-routes.md).
# Runs where the database is: locally, or on the VM through SSH (bash -s).
# Usage: seed.sh <bethere> <db> <date YYYY-MM-DD> <count> <rundown CSV, base64> [first index]
# Prints one line per link: "<event index> <role name> <token>". Tokens are
# test credentials of a test database, never of the real one.
set -euo pipefail
BIN="$1" DB="$2" DATE="$3" COUNT="$4" CSV_B64="$5" FIRST="${6:-1}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
echo "$CSV_B64" | base64 -d > "$TMP/rundown.csv"
token() { grep -o '#t=[A-Za-z0-9_-]*' | head -1 | cut -c4-; }

for i in $(seq "$FIRST" $((FIRST + COUNT - 1))); do
  master="$("$BIN" create-event -db "$DB" -name "E2E $i" -date "$DATE" -tz Asia/Taipei \
    -master-name 總召 -base-url https://e2e.invalid | token)"
  echo "$i 總召 $master"
  for pair in 新秘:Stylist 攝影:Media 音控:StageAV 新人:Host; do
    name="${pair%%:*}" type="${pair##*:}"
    tok="$(echo "$master" | "$BIN" add-role -db "$DB" -name "$name" -type "$type" -base-url https://e2e.invalid 2>/dev/null | token)"
    echo "$i $name $tok"
  done
  echo "$master" | "$BIN" import-rundown -db "$DB" -file "$TMP/rundown.csv" >/dev/null 2>&1
done
