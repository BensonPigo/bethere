#!/usr/bin/env bash
# Switch the VM to a release (deploy or roll back). Usage: deploy.sh <bethere-version>
set -euo pipefail
source "$(dirname "$0")/env.sh"
VERSION="${1:?usage: deploy.sh <bethere-version>}"

gc storage objects describe "gs://$RELEASE_BUCKET/bethere/$VERSION/bethere" >/dev/null
gc compute instances add-metadata "$VM" --zone "$ZONE" --metadata "bethere-version=$VERSION"
gc compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command "sudo systemctl restart bethere && systemctl is-active bethere"
