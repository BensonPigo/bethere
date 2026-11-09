#!/usr/bin/env bash
# Maintenance mode (US-O-2): only the Master gets in. Usage: maintenance.sh on|off|status
#   on      set metadata bethere-maintenance=1 and restart
#   off     set it to 0, remove the restore marker, restart; run only after
#           the Master has checked the data (operations/runbook.md section 2)
#   status  show metadata, the marker and the mode the service runs in
# Each ends by checking the mode from outside.
set -euo pipefail
source "$(dirname "$0")/env.sh"
MARKER=/var/lib/bethere/maintenance.forced
MEMBER_RESET=/var/lib/bethere/member-reset.pending

case "${1:-}" in
  on) want=1 ;;
  off) want=0 ;;
  status) want="" ;;
  *) echo "usage: maintenance.sh on|off|status" >&2; exit 2 ;;
esac

if [ -n "$want" ]; then
  gc compute instances add-metadata "$VM" --zone "$ZONE" --metadata "bethere-maintenance=$want"
  rm_marker=""
  if [ "$want" = 0 ]; then rm_marker="sudo rm -f $MARKER"; fi
  vm_ssh "set -e
    $rm_marker
    sudo systemctl restart bethere
    sleep 3
    systemctl is-active bethere >/dev/null" || {
    echo "metadata bethere-maintenance is now $want, but the restart failed; run maintenance.sh status (if ssh itself fails, check the IAP permission and firewall: the service keeps its mode until this succeeds)" >&2
    exit 1
  }
fi

state="$(vm_ssh "cat /run/bethere/maintenance.env 2>/dev/null; if [ -e $MARKER ]; then echo marker=present; else echo marker=absent; fi; if [ -e $MEMBER_RESET ]; then echo member_reset=pending; fi")"
echo "metadata bethere-maintenance=$(gc compute instances describe "$VM" --zone "$ZONE" \
  --format='value(metadata.items.bethere-maintenance)')"
echo "$state"
if grep -qx member_reset=pending <<<"$state"; then
  echo "成員連結尚未清除: company pages and console links stay closed (the wedding is not affected); see journalctl -u bethere | grep member-reset, then restart the service with a release that clears them (US-O-12.6)" >&2
fi
running="$(sed -n 's/^BETHERE_MAINTENANCE=//p' <<<"$state")"
if [ -z "$running" ]; then
  echo "the service has not started since boot (no /run/bethere/maintenance.env); see journalctl -u bethere" >&2
  exit 1
fi
if [ -n "$want" ] && [ "$running" != "$want" ]; then
  echo "the service runs with maintenance $running, not $want (the restore marker keeps it on until off)" >&2
  exit 1
fi
probe_mode "$running"
