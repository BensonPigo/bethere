#!/usr/bin/env bash
# Switch the VM to a release (deploy or roll back). Usage: deploy.sh <bethere-version>
# The VM's start script and units are laid down again from this checkout
# first, so the service always runs with the ones in the repository. Then
# the release, its version and the mode it started in are checked: from the
# VM, and from outside, since /api/ping is closed in maintenance mode.
set -euo pipefail
source "$(dirname "$0")/env.sh"
VERSION="${1:?usage: deploy.sh <bethere-version>}"
if ! [[ "$VERSION" =~ ^[0-9a-f]+-[0-9]{14}$ ]]; then
  echo "not a release version: $VERSION (e.g. c406027-20261027032903)" >&2
  exit 2
fi

gc storage objects describe "gs://$RELEASE_BUCKET/bethere/$VERSION/bethere" >/dev/null
gc compute instances add-metadata "$VM" --zone "$ZONE" --metadata "bethere-version=$VERSION" \
  --metadata-from-file startup-script="$(dirname "$0")/vm-startup.sh"
state="$(vm_ssh "set -e
  sudo google_metadata_script_runner startup >/dev/null 2>&1
  sudo systemctl restart bethere
  sleep 3
  systemctl is-active bethere >/dev/null
  echo version=\$(/opt/bethere/bethere version)
  cat /run/bethere/maintenance.env")" || {
  echo "the service did not start with $VERSION (the VM's error is above; while maintenance mode is on, a release without it is refused; ops/gcp/maintenance.sh status)" >&2
  exit 1
}
echo "$state"
grep -qxF "version=$VERSION" <<<"$state" || { echo "running version is not $VERSION" >&2; exit 1; }
probe_mode "$(sed -n 's/^BETHERE_MAINTENANCE=//p' <<<"$state")"
