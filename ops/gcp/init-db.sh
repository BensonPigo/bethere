#!/usr/bin/env bash
# Create the database on the VM, once, on the very first deployment
# (tech-stack.md 2.1「不自動建庫」). bethere refuses when the file already
# exists, so running this again changes nothing. Never use it after a
# failed restore: restore again instead (ADR-0012).
set -euo pipefail
source "$(dirname "$0")/env.sh"

gc compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command '
  set -e
  sudo systemctl stop bethere
  sudo /opt/bethere/prepare.sh bethere   # install the release in metadata first
  sudo -u bethere env BETHERE_INIT=1 timeout 10 /opt/bethere/bethere serve -db /var/lib/bethere/bethere.db -addr 127.0.0.1:18081 || true
  test -s /var/lib/bethere/bethere.db
  sudo systemctl start bethere && systemctl is-active bethere'
