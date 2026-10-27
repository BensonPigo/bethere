#!/usr/bin/env bash
# Create the single VM (tech-stack.md section 3). Usage: create-vm.sh <bethere-version>
# It starts in maintenance mode: after a restore only the Master gets in
# until ops/gcp/maintenance.sh off (US-O-2, operations/runbook.md section 2).
# DOMAIN defaults to the static IP's sslip.io name; PREVIOUS_VERSION, when
# set, is kept as the version to roll back to (ops/gcp/rebuild.sh sets both).
set -euo pipefail
source "$(dirname "$0")/env.sh"
VERSION="${1:?usage: create-vm.sh <bethere-version>}"
check_release "$VERSION" || exit 2

count="$(gc compute instances list --format='value(name)' | wc -l | tr -d ' ')"
if [ "$count" != "0" ]; then
  echo "refusing: project already has $count VM(s); there must be exactly one" >&2
  exit 1
fi

IP="$(gc compute addresses describe "$ADDRESS" --region "$REGION" --format='value(address)')"
DOMAIN="${DOMAIN:-${IP//./-}.sslip.io}"
domain_ok "$DOMAIN" || { echo "not a host name: $DOMAIN" >&2; exit 2; }
# A new boot disk for every VM, named apart from the disks of earlier VMs,
# which are kept when those are deleted.
DISK="$VM-$(date -u +%Y%m%d%H%M%S)"
previous=""
if [ -n "${PREVIOUS_VERSION:-}" ]; then
  version_ok "$PREVIOUS_VERSION" || { echo "not a release version: PREVIOUS_VERSION=$PREVIOUS_VERSION" >&2; exit 2; }
  previous=",bethere-previous-version=$PREVIOUS_VERSION"
fi

gc compute instances create "$VM" --zone "$ZONE" \
  --machine-type e2-micro \
  --create-disk "boot=yes,name=$DISK,image-family=debian-12,image-project=debian-cloud,size=10GB,type=pd-balanced,auto-delete=no" \
  --subnet "$SUBNET" --address "$IP" --tags bethere-web \
  --service-account "$SA_EMAIL" --scopes cloud-platform \
  --maintenance-policy MIGRATE --restart-on-failure \
  --shielded-secure-boot --shielded-vtpm --shielded-integrity-monitoring \
  --metadata "bethere-version=$VERSION$previous,caddy-version=$CADDY_VERSION,litestream-version=$LITESTREAM_VERSION,release-bucket=$RELEASE_BUCKET,backup-bucket=$BACKUP_BUCKET,bethere-domain=$DOMAIN,bethere-maintenance=1" \
  --metadata-from-file startup-script="$(dirname "$0")/vm-startup.sh"

echo "disk: $DISK"
echo "https://$DOMAIN/"
