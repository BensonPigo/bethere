#!/usr/bin/env bash
# Rebuild the VM on a new, empty disk after a VM or disk failure (US-O-6.2,
# operations/runbook.md section 2). Usage: rebuild.sh <bethere-version> [zone]
# The new VM restores the database from the backup bucket and starts in
# maintenance mode; the Master checks the data before maintenance.sh off.
# Everything that can be checked is checked before the old VM is deleted.
# Safe to run again after a failure: it deletes whatever VM is there.
# -E: the STOPPED message also fires for a failure inside a function (gc).
set -Eeuo pipefail
# The zone is decided below, after listing the VMs; a ZONE left in the
# shell never counts, and this placeholder skips env.sh's own lookup.
ZONE=unset
source "$(dirname "$0")/env.sh"
VERSION="${1:?usage: rebuild.sh <bethere-version> [zone]}"
started=$SECONDS
step="checks" advice="This run deleted nothing; fix the cause and run it again."
# Printed by the main shell only: a failure inside $(...) is reported once,
# where its result is used.
trap '[ "$BASH_SUBSHELL" != 0 ] || echo "STOPPED at: $step ($(( (SECONDS - started) / 60 )) min in). $advice" >&2' ERR

# ---- checks, before anything is deleted
check_release "$VERSION"
PROBE_SECONDS="${PROBE_SECONDS:-600}"
[[ "$PROBE_SECONDS" =~ ^[1-9][0-9]*$ ]] || { echo "PROBE_SECONDS must be a number of seconds" >&2; false; }
gc compute addresses describe "$ADDRESS" --region "$REGION" --format='value(address)' >/dev/null
if ! backups="$(gc storage ls "gs://$BACKUP_BUCKET/bethere/" 2>&1)" || [ -z "$backups" ]; then
  if grep -q "matched no objects" <<<"$backups"; then
    echo "gs://$BACKUP_BUCKET/bethere/ is empty: there is nothing to restore" >&2
  else
    echo "cannot list gs://$BACKUP_BUCKET/bethere/ (permission?): $backups" >&2
  fi
  false
fi

# The old VM's domain and rollback version carry over, and a value left in
# the shell (from a drill or an earlier rerun) must not replace them. With
# no VM left (a rerun after the delete) both come from the paper runbook;
# PREVIOUS_VERSION=none when there is no rollback version.
vms="$(gc compute instances list --format='value(name,zone.basename(),status,creationTimestamp)')"
zone=""
if [ -n "$vms" ]; then read -r name zone _ <<<"$(head -1 <<<"$vms")"; fi
# The new VM goes to the zone given, else where the old one is.
ZONE="${2:-${zone:-asia-east1-b}}"
[[ "$ZONE" == "$REGION"-? ]] || { echo "zone $ZONE is not in $REGION" >&2; false; }
gc compute machine-types describe e2-micro --zone "$ZONE" >/dev/null ||
  { echo "zone $ZONE does not exist or has no e2-micro" >&2; false; }
if [ -n "$vms" ]; then
  item() { gc compute instances describe "$name" --zone "$zone" --format="value(metadata.items.$1)"; }
  old_domain="$(item bethere-domain)"
  old_version="$(item bethere-version)"
  old_previous="$(item bethere-previous-version)"
  if [ "$old_version" != "$VERSION" ]; then old_previous="$old_version"; fi
  # A record that is not a version (edited by hand) cannot be rolled back to.
  version_ok "$old_previous" || old_previous=""
  old_previous="${old_previous:-none}"
  [ -z "${DOMAIN:-}" ] || [ "$DOMAIN" = "$old_domain" ] ||
    { echo "DOMAIN=$DOMAIN differs from the VM's $old_domain; unset DOMAIN" >&2; false; }
  [ -z "${PREVIOUS_VERSION:-}" ] || [ "$PREVIOUS_VERSION" = "$old_previous" ] ||
    { echo "PREVIOUS_VERSION=$PREVIOUS_VERSION differs from the VM's $old_previous; unset PREVIOUS_VERSION" >&2; false; }
  DOMAIN="$old_domain" PREVIOUS_VERSION="$old_previous"
elif [ -z "${DOMAIN:-}" ] || [ -z "${PREVIOUS_VERSION:-}" ]; then
  echo "no VM to read them from: set DOMAIN and PREVIOUS_VERSION (or none) from the paper runbook" >&2
  false
fi
domain_ok "$DOMAIN" || { echo "not a host name: $DOMAIN (no https://, no slash, lower case)" >&2; false; }
if [ "$PREVIOUS_VERSION" != none ]; then
  version_ok "$PREVIOUS_VERSION" || { echo "not a release version: PREVIOUS_VERSION=$PREVIOUS_VERSION" >&2; false; }
  if ! gc storage objects describe "gs://$RELEASE_BUCKET/bethere/$PREVIOUS_VERSION/bethere" >/dev/null 2>&1; then
    # A typed value is a mistake; a recorded one whose release was removed
    # can no longer be rolled back to, which must not block the rebuild.
    [ -n "$vms" ] || { echo "the rollback version $PREVIOUS_VERSION is not in gs://$RELEASE_BUCKET" >&2; false; }
    echo "warning: the recorded rollback version $PREVIOUS_VERSION is no longer in gs://$RELEASE_BUCKET; rebuilding without one" >&2
    PREVIOUS_VERSION=none
  fi
fi
# The whole command to run again once the old VM is gone, zone included.
rerun="PROJECT=$PROJECT DOMAIN=$DOMAIN PREVIOUS_VERSION=$PREVIOUS_VERSION $0 $VERSION $ZONE"

echo "rebuild in $PROJECT, zone $ZONE, version $VERSION (rollback: $PREVIOUS_VERSION), domain $DOMAIN"
echo "VMs that will be deleted (their disks are kept):"
echo "${vms:-(none)}" | sed 's/^/  /'
read -rp "type the project ID to go on: " answer
if [ "$(tr -d '\r' <<<"$answer")" != "$PROJECT" ]; then echo "not confirmed; nothing changed" >&2; exit 1; fi

# ---- delete every VM, keeping its disk
step="delete the old VM" advice="Run it again: $rerun (if the VM cannot be deleted, give up; runbook section 2)."
while read -r name zone _; do
  [ -n "$name" ] || continue
  echo "deleting $name in $zone ..."
  gc compute instances delete "$name" --zone "$zone" --keep-disks=all </dev/null
done <<<"$vms"

# ---- one new VM on a new disk
step="create the new VM"
advice="Run it again: $rerun (or with another zone at the end if this one has no e2-micro)."
previous="$PREVIOUS_VERSION"; [ "$previous" != none ] || previous=""
ZONE="$ZONE" DOMAIN="$DOMAIN" PREVIOUS_VERSION="$previous" "$(dirname "$0")/create-vm.sh" "$VERSION"

# ---- from outside: certificate valid, restored, in maintenance mode
step="wait for https://$DOMAIN in maintenance mode (boot, restore, certificate)"
advice="The new VM may still be coming up: check PROJECT=$PROJECT ops/gcp/maintenance.sh status in a few minutes before running it again."
echo "$step; up to $PROBE_SECONDS seconds ..."
probe_mode 1

echo "rebuilt in $(( (SECONDS - started) / 60 )) min. Next: runbook section 2, steps 5-6 (the Master checks, then maintenance.sh off)."
echo "Kept disks (delete after the event, runbook section 4):"
gc compute disks list --filter='-users:*' --format='value(name,zone.basename())' | sed 's/^/  /' || true
