#!/usr/bin/env bash
# Switch the VM to a release (deploy or roll back). Usage: deploy.sh [<bethere-version>]
# Without a version it only lists: the running version, the one it replaced
# (the one to roll back to, US-O-6.1) and the newest uploads.
# The VM's start script and units are laid down again from this checkout
# first, so the service always runs with the ones in the repository. Then
# the release, its version and the mode it started in are checked: from the
# VM, and from outside, since /api/ping is closed in maintenance mode.
set -euo pipefail
source "$(dirname "$0")/env.sh"

meta() { gc compute instances describe "$VM" --zone "$ZONE" --format="value(metadata.items.$1)"; }
current="$(meta bethere-version)"

# label: whether a version may be deployed (check_release, mail-login).
label() { if mail_login "$1"; then echo "mail-login"; else echo "EMAIL-ONLY: cannot be deployed"; fi; }

if [ $# -eq 0 ]; then
  previous="$(meta bethere-previous-version)"
  echo "running:  $current   ($(label "$current"))"
  echo "previous: $previous   ($(label "$previous"))   <- roll back to this one"
  echo "newest uploads (not all were deployed):"
  gc storage ls "gs://$RELEASE_BUCKET/bethere/" | sed 's#.*/bethere/##; s#/$##' | sort -t- -k2 -r | head -5 | sed 's/^/  /'
  exit 0
fi
VERSION="$1"
check_release "$VERSION" || exit 2

# The replaced version is what a rollback goes back to; deploying the same
# one again keeps the record.
previous=""
if version_ok "$current" && [ "$current" != "$VERSION" ]; then previous=",bethere-previous-version=$current"; fi
gc compute instances add-metadata "$VM" --zone "$ZONE" --metadata "bethere-version=$VERSION$previous" \
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
