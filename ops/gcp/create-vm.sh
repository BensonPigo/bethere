#!/usr/bin/env bash
# Create the single VM (tech-stack.md section 3). Usage: create-vm.sh <bethere-version>
set -euo pipefail
source "$(dirname "$0")/env.sh"
VERSION="${1:?usage: create-vm.sh <bethere-version>}"

count="$(gc compute instances list --format='value(name)' | wc -l | tr -d ' ')"
if [ "$count" != "0" ]; then
  echo "refusing: project already has $count VM(s); there must be exactly one" >&2
  exit 1
fi

IP="$(gc compute addresses describe "$ADDRESS" --region "$REGION" --format='value(address)')"
DOMAIN="${DOMAIN:-${IP//./-}.sslip.io}"

gc compute instances create "$VM" --zone "$ZONE" \
  --machine-type e2-micro \
  --image-family debian-12 --image-project debian-cloud \
  --boot-disk-size 10GB --boot-disk-type pd-balanced --no-boot-disk-auto-delete \
  --subnet "$SUBNET" --address "$IP" --tags bethere-web \
  --service-account "$SA_EMAIL" --scopes cloud-platform \
  --maintenance-policy MIGRATE --restart-on-failure \
  --shielded-secure-boot --shielded-vtpm --shielded-integrity-monitoring \
  --metadata "bethere-version=$VERSION,caddy-version=$CADDY_VERSION,litestream-version=$LITESTREAM_VERSION,release-bucket=$RELEASE_BUCKET,backup-bucket=$BACKUP_BUCKET,bethere-domain=$DOMAIN" \
  --metadata-from-file startup-script="$(dirname "$0")/vm-startup.sh"

echo "https://$DOMAIN/"
