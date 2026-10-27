#!/usr/bin/env bash
# External monitoring (US-O-7, ADR-0024): an uptime check that opens
# https://<domain>/healthz every minute from outside, and an alert that emails
# ALERT_EMAIL when it fails for 2 minutes and again when it recovers. Nothing
# is installed on the VM. Usage: ALERT_EMAIL=you@example.com monitoring.sh
# Safe to run again: the check is remade when the VM's domain changed, the
# alert is remade every time (to send to ALERT_EMAIL), and it is on only
# while the VM runs; ops/gcp/power.sh turns it on and off with the VM.
# -E: the ERR trap below also fires for a failure inside gc.
set -Eeuo pipefail
source "$(dirname "$0")/env.sh"
CHECK="bethere-healthz" POLICY="bethere down"

EMAIL="${ALERT_EMAIL:?set ALERT_EMAIL, the address the alerts go to}"
[[ "$EMAIL" =~ ^[^@[:space:]\"\\]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || { echo "not an email address: $EMAIL" >&2; exit 2; }
DOMAIN="$(gc compute instances describe "$VM" --zone "$ZONE" --format='value(metadata.items.bethere-domain)')"
domain_ok "$DOMAIN" || { echo "the VM has no domain to check: $DOMAIN" >&2; exit 1; }
running="$(gc compute instances describe "$VM" --zone "$ZONE" --format='value(status)')"

# The email channel; gcloud has no GA command for channels, so the API.
api="https://monitoring.googleapis.com/v3/projects/$PROJECT/notificationChannels"
token="$(gcloud auth print-access-token)"
# The token goes in through a file descriptor, not the command line (ps).
call() { curl -fsS -H @<(printf 'Authorization: Bearer %s\n' "$token") -H 'Content-Type: application/json' "$@"; }
# No match is an answer here (no channel yet), not a failure.
name_of() { { grep -o '"projects/[^"]*/notificationChannels/[0-9]*"' || true; } | head -1 | tr -d '"'; }
channel="$(call -G "$api" --data-urlencode "filter=type=\"email\" AND labels.email_address=\"$EMAIL\"" | name_of)"
if [ -z "$channel" ]; then
  channel="$(call -X POST "$api" \
    -d "{\"type\":\"email\",\"displayName\":\"BeThere $EMAIL\",\"labels\":{\"email_address\":\"$EMAIL\"}}" | name_of)"
fi
[ -n "$channel" ] || { echo "could not find or create the email channel for $EMAIL" >&2; exit 1; }

old_policies="$(gc monitoring policies list --filter="displayName=\"$POLICY\"" --format='value(name)')"
check_of() { gc monitoring uptime list-configs --filter="displayName=$CHECK" --format='value(name,monitoredResource.labels.host)'; }
# A failed listing must stop here, not read as "no check" and make another.
checks="$(check_of)"
read -r check host <<<"$checks"
if [ -n "$check" ] && [ "$host" != "$DOMAIN" ]; then
  # The alert refers to the check, so it goes first; until this run ends
  # there is no alert, and a failure here says so.
  trap '[ "$BASH_SUBSHELL" != 0 ] || echo "the alert was removed with the old check; run monitoring.sh again" >&2' ERR
  for p in $old_policies; do gc monitoring policies delete "$p" >/dev/null; done
  old_policies=""
  gc monitoring uptime delete "$check" >/dev/null
  check=""
fi
if [ -z "$check" ]; then
  gc monitoring uptime create "$CHECK" --resource-type=uptime-url \
    --resource-labels="host=$DOMAIN,project_id=$PROJECT" \
    --protocol=https --path=/healthz --period=1 --timeout=10 --validate-ssl=true >/dev/null
  checks="$(check_of)"
  read -r check host <<<"$checks"
  [ -n "$check" ] || { echo "the uptime check was not created" >&2; exit 1; }
fi

# Alert: more than one region fails for 2 minutes; mail on open and close.
enabled=false; [ "$running" = RUNNING ] && enabled=true
policy="$(mktemp)"
cat > "$policy" <<JSON
{
  "displayName": "$POLICY",
  "combiner": "OR",
  "enabled": $enabled,
  "notificationChannels": ["$channel"],
  "alertStrategy": {"autoClose": "1800s", "notificationPrompts": ["OPENED", "CLOSED"]},
  "documentation": {"mimeType": "text/markdown",
    "content": "https://$DOMAIN/healthz does not answer. Open it on mobile data first; then operations/runbook.md section 2."},
  "conditions": [{
    "displayName": "https://$DOMAIN/healthz fails",
    "conditionThreshold": {
      "filter": "metric.type=\"monitoring.googleapis.com/uptime_check/check_passed\" AND metric.label.check_id=\"${check##*/}\" AND resource.type=\"uptime_url\"",
      "aggregations": [{"alignmentPeriod": "60s", "perSeriesAligner": "ALIGN_NEXT_OLDER",
        "crossSeriesReducer": "REDUCE_COUNT_FALSE", "groupByFields": ["resource.label.host"]}],
      "comparison": "COMPARISON_GT", "thresholdValue": 1, "duration": "120s",
      "trigger": {"count": 1}
    }
  }]
}
JSON
# The new alert is made before the old ones go, so a failure leaves the old.
gc monitoring policies create --policy-from-file="$policy" >/dev/null
trap - ERR
rm -f "$policy"
for p in $old_policies; do gc monitoring policies delete "$p" >/dev/null; done

echo "check: https://$DOMAIN/healthz every minute"
echo "alert: to $EMAIL, $([ "$enabled" = true ] && echo on || echo "off (the VM is $running; power.sh on turns it on)")"
