#!/usr/bin/env bash
# Set up the VM's test service for the end-to-end routes, once (ADR-0029):
# its domain, its systemd unit (from vm-startup.sh) and its own database.
# The real service and database are not touched; Caddy restarts once
# (about a second) to add the test domain. Running it again is harmless.
set -euo pipefail
source "$(dirname "$0")/env.sh"

DOMAIN="$(gc compute instances describe "$VM" --zone "$ZONE" \
  --format='value(metadata.items.bethere-domain)')"
E2E_DOMAIN="${E2E_DOMAIN:-e2e.$DOMAIN}"

gc compute instances add-metadata "$VM" --zone "$ZONE" \
  --metadata "bethere-e2e-domain=$E2E_DOMAIN" \
  --metadata-from-file startup-script="$(dirname "$0")/vm-startup.sh"

gc compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command '
  set -e
  sudo google_metadata_script_runner startup
  if [ ! -s /var/lib/bethere-e2e/e2e.db ]; then
    sudo -u bethere env BETHERE_INIT=1 timeout 5 /opt/bethere/bethere serve -db /var/lib/bethere-e2e/e2e.db -addr 127.0.0.1:18082 || true
  fi
  test -s /var/lib/bethere-e2e/e2e.db
  sudo systemctl restart caddy
  sudo systemctl restart bethere-e2e
  systemctl is-active bethere bethere-e2e caddy'

echo "test service: https://$E2E_DOMAIN/"
echo "run the routes: cd web && E2E_TARGET=vm E2E_BASE_URL=https://$E2E_DOMAIN npm run e2e"
