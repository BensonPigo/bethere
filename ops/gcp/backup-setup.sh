#!/usr/bin/env bash
# Turn on backups for a VM created before them, once (US-O-1): name the
# Litestream version and the backup bucket in metadata, lay down the new
# systemd unit, and restart bethere under Litestream. Run setup.sh and
# release.sh first so the bucket and the Litestream mirror exist. Running it
# again is harmless.
set -euo pipefail
source "$(dirname "$0")/env.sh"

gc storage objects describe "gs://$RELEASE_BUCKET/litestream/$LITESTREAM_VERSION/litestream" >/dev/null
gc storage buckets describe "gs://$BACKUP_BUCKET" >/dev/null
gc compute instances add-metadata "$VM" --zone "$ZONE" \
  --metadata "litestream-version=$LITESTREAM_VERSION,backup-bucket=$BACKUP_BUCKET" \
  --metadata-from-file startup-script="$(dirname "$0")/vm-startup.sh"

gc compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command '
  set -e
  sudo google_metadata_script_runner startup
  sudo systemctl restart bethere
  sleep 3
  systemctl is-active bethere'
