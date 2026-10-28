#!/usr/bin/env bash
# The support address shown on the way in and to the Master (US-O-8,
# ADR-0037). Usage: support-email.sh <address>|none
# It is project metadata, shared by every VM, so a rebuilt VM keeps it. The
# service reads it when it starts (deploy.sh, a VM start, a reboot); this
# script never restarts it, so changing a label cannot take the site down.
# Writing project metadata needs the project owner, not the backup operator.
set -euo pipefail
source "$(dirname "$0")/env.sh"
KEY="bethere-support-email"

v="${1:?usage: support-email.sh <address>|none}"
if [ "$v" = none ]; then
  gc compute project-info remove-metadata --keys="$KEY" >/dev/null
else
  # The same pattern as supportAddress in cmd/bethere/main.go.
  [[ "$v" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$ ]] ||
    { echo "not one email address: $v" >&2; exit 2; }
  gc compute project-info add-metadata --metadata "$KEY=$v" >/dev/null
fi
echo "set to $v; the site shows it from the service's next start: ops/gcp/deploy.sh <the running version> restarts it, power.sh on only when the VM was off"
