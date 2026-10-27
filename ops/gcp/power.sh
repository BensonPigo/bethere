#!/usr/bin/env bash
# Turn the VM and its alert on or off together (US-O-7): during development
# the VM is off between uses, and the alert must not mail about that.
# Usage: power.sh on|off|status. From the freeze on, the VM stays on.
set -euo pipefail
source "$(dirname "$0")/env.sh"
POLICY="bethere down"

policies() { gc monitoring policies list --filter="displayName=\"$POLICY\"" --format='value(name)'; }
# alert on|off: no alert yet is only a warning, so power still works.
alert() {
  local list p found=""
  list="$(policies)"
  for p in $list; do
    gc monitoring policies update "$p" "--$([ "$1" = on ] && echo enabled || echo no-enabled)" >/dev/null
    found=1
  done
  if [ -n "$found" ]; then alert_state="alert $1"; else
    alert_state="no alert"; echo "warning: there is no alert yet; run ops/gcp/monitoring.sh" >&2
  fi
}
status() { gc compute instances describe "$VM" --zone "$ZONE" --format='value(status)'; }

case "${1:-}" in
  on)
    gc compute instances start "$VM" --zone "$ZONE" >/dev/null
    domain="$(gc compute instances describe "$VM" --zone "$ZONE" --format='value(metadata.items.bethere-domain)')"
    wait="${PROBE_SECONDS:-300}"
    [[ "$wait" =~ ^[1-9][0-9]*$ ]] || { echo "PROBE_SECONDS must be a number of seconds" >&2; exit 2; }
    deadline=$((SECONDS + wait))
    until [ "$(curl -s -m 5 -o /dev/null -w '%{http_code}' "https://$domain/healthz" || true)" = 200 ]; do
      [ "$SECONDS" -lt "$deadline" ] ||
        { echo "the VM is on but https://$domain/healthz does not answer after $wait seconds; the alert stays off: run power.sh on again once it answers" >&2; exit 1; }
      sleep 2
    done
    alert on
    checked="$(gc monitoring uptime list-configs --filter="displayName=bethere-healthz" --format='value(monitoredResource.labels.host)')"
    [ -z "$checked" ] || [ "$checked" = "$domain" ] ||
      echo "warning: the check opens $checked, not $domain; run ops/gcp/monitoring.sh" >&2
    echo "on: https://$domain/ ($alert_state)" ;;
  off)
    # The alert goes off first: the other way round, stopping mails an alert.
    alert off
    gc compute instances stop "$VM" --zone "$ZONE" >/dev/null
    echo "off: VM $(status) ($alert_state)" ;;
  status)
    echo "VM: $(status)"
    echo "alert enabled: $(gc monitoring policies list --filter="displayName=\"$POLICY\"" --format='value(enabled)' | tr '\n' ' ')" ;;
  *) echo "usage: power.sh on|off|status" >&2; exit 2 ;;
esac
