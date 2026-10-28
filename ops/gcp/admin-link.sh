#!/usr/bin/env bash
# Make the admin page's link on the real database (US-O-9.1, ADR-0038).
# The link is printed here once; the last one stops working. Run it again
# whenever the link may have leaked, and after every restore from backup.
# The service keeps running: the command takes the write lock briefly.
set -euo pipefail
source "$(dirname "$0")/env.sh"

domain="$(gc compute instances describe "$VM" --zone "$ZONE" \
  --format='value(metadata.items.bethere-domain)')"
domain_ok "$domain" || { echo "the VM has no usable bethere-domain metadata: '$domain'" >&2; exit 1; }
vm_ssh "sudo -u bethere /opt/bethere/bethere issue-admin-link -db /var/lib/bethere/bethere.db -base-url https://$domain"
