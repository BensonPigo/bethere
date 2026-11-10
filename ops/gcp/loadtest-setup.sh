#!/usr/bin/env bash
# Make the load test's data on the drill VM (US-O-13.2): 3 events dated
# today, each with the Master and 49 more roles and a 60-cue rundown, all
# with the existing commands. Prints the links for cmd/loadtest, one
# "<event> <master|role> <link>" per line. Never on the real project.
set -euo pipefail
source "$(dirname "$0")/env.sh"
if [ "$PROJECT" = "bethere-prod-0606" ]; then
  echo "refusing: the load test never runs on the real project" >&2
  exit 2
fi
DOMAIN="$(gc compute instances describe "$VM" --zone "$ZONE" --format='value(metadata.items.bethere-domain)')"
domain_ok "$DOMAIN" || { echo "the VM has no domain: $DOMAIN" >&2; exit 1; }

vm_ssh "BASE=https://$DOMAIN bash -s" <<'REMOTE'
set -euo pipefail
B=/opt/bethere/bethere
DB=/var/lib/bethere/bethere.db
DAY="$(TZ=Asia/Taipei date +%F)"
link() { grep -o 'https://[^ ]*#t=[A-Za-z0-9_-]*' | head -1; }
types=(Stylist Media StageAV)
csv="$(mktemp)"
chmod 644 "$csv"
{
  echo "seq,title,start,duration_min,roles"
  for k in $(seq 1 60); do
    m=$((480 + 5 * (k - 1)))
    a=$((3 + (3 * k) % 47)); b=$((3 + (3 * k + 1) % 47)); c=$((3 + (3 * k + 2) % 47))
    printf '%d,環節%d,%02d:%02d,5,角色%d;角色%d;角色%d\n' "$k" "$k" $((m / 60)) $((m % 60)) "$a" "$b" "$c"
  done
} >"$csv"
for e in 1 2 3; do
  master="$(sudo -u bethere "$B" create-event -db "$DB" -name "負載測試 $e" -date "$DAY" \
    -tz Asia/Taipei -master-name 總召 -base-url "$BASE" | link)"
  echo "$e master $master"
  for i in $(seq 1 49); do
    if [ "$i" -le 2 ]; then t=Host; else t="${types[$((i % 3))]}"; fi
    role="$(echo "$master" | sudo -u bethere "$B" add-role -db "$DB" -name "角色$i" \
      -type "$t" -base-url "$BASE" 2>/dev/null | link)"
    [ -n "$role" ] || { echo "event $e: adding 角色$i failed" >&2; exit 1; }
    echo "$e role $role"
  done
  echo "$master" | sudo -u bethere "$B" import-rundown -db "$DB" -file "$csv" >/dev/null 2>&1 ||
    { echo "event $e: importing the rundown failed" >&2; exit 1; }
done
rm -f "$csv"
REMOTE
