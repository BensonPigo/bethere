#!/usr/bin/env bash
# Tests of the ops scripts against a fake gcloud (ops/gcp/test/fake): every
# check of rebuild.sh stops before the old VM is deleted, and a rebuild
# carries the old VM's settings over (US-O-6). Run: ops/gcp/test/run.sh
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
OPS="$(dirname "$HERE")"
V=abc1234-20261027000000 P=aaa0000-20261001000000 N=def0000-20261027010000
failed=0

# fresh: a project with one VM in asia-east1-c running $V, rollback $P.
fresh() {
  FAKE="$(mktemp -d)"; export FAKE
  touch "$FAKE/vm"; echo asia-east1-c > "$FAKE/vm.zone"
  echo "$V" > "$FAKE/meta.bethere-version"
  echo "$P" > "$FAKE/meta.bethere-previous-version"
  echo bethere.example > "$FAKE/meta.bethere-domain"
  : > "$FAKE/calls"
}
run() { PATH="$HERE/fake:$PATH" "$@" > "$FAKE/out" 2>&1; }
rebuild() { echo "${CONFIRM:-test-proj}" | run env PROJECT=test-proj "$@" "$OPS/rebuild.sh" "${ARGS[@]}"; }
called() { grep -q -- "$1" "$FAKE/calls"; }
said() { grep -q -- "$1" "$FAKE/out"; }
deleted() { called "instances delete"; }
created() { called "instances create"; }
create_has() { grep "instances create" "$FAKE/calls" | grep -qE -- "$1"; }
# stopped: stopped at the checks for the reason given, nothing deleted or created.
stopped() { ! deleted && ! created && said "$1" && said "This run deleted nothing"; }
check() { # name, condition (shell code)
  if eval "$2"; then echo "ok   $1"; else echo "FAIL $1"; sed 's/^/     /' "$FAKE/out"; failed=1; fi
}

fresh; run env -u PROJECT "$OPS/rebuild.sh" "$V" </dev/null
check "no PROJECT: refused, nothing called" '! test -s "$FAKE/calls" && said "set PROJECT first"'
fresh; run env -u PROJECT "$OPS/maintenance.sh" off
check "maintenance without PROJECT: refused" '! test -s "$FAKE/calls" && said "set PROJECT first"'

ARGS=(not-a-version); fresh; rebuild
check "bad version: stops before deleting" 'stopped "not a release version"'
ARGS=("$V"); fresh; echo "caddy/" > "$FAKE/missing"; rebuild
check "Caddy build missing: stops before deleting" 'stopped "missing gs://.*caddy"'
fresh; touch "$FAKE/nobackup"; rebuild
check "empty backup: stops before deleting, STOPPED printed once" 'stopped "nothing to restore" && [ "$(grep -c STOPPED "$FAKE/out")" = 1 ]'
fresh; rebuild PROBE_SECONDS=10m
check "bad PROBE_SECONDS: stops before deleting" 'stopped "PROBE_SECONDS"'
fresh; touch "$FAKE/noperm"; rebuild
check "backup not listable: says so, stops before deleting" 'stopped "cannot list"'
ARGS=("$V" asia-east2-a); fresh; rebuild
check "zone outside the region: stops before deleting" 'stopped "not in asia-east1"'
fresh; touch "$FAKE/noaddress"; rebuild
check "static IP missing: stops before deleting" 'stopped "STOPPED at: checks"'
ARGS=("$V" asia-east1-d); fresh; rebuild
check "zone that does not exist: stops before deleting" 'stopped "does not exist"'
ARGS=("$V"); fresh; CONFIRM=other-proj rebuild
check "wrong project typed: nothing deleted" '! deleted && said "not confirmed"'
fresh; rebuild DOMAIN=drill.example
check "DOMAIN left in the shell: stops before deleting" 'stopped "unset DOMAIN"'
fresh; rebuild PREVIOUS_VERSION=fff0000-20200101000000
check "PREVIOUS_VERSION left in the shell: stops before deleting" 'stopped "unset PREVIOUS_VERSION"'
fresh; echo "bethere/$P" > "$FAKE/missing"; rebuild
check "recorded rollback version removed from the bucket: warns, rebuilds without one" \
  'said "no longer in" && created && ! create_has "bethere-previous-version"'
fresh; rm "$FAKE/vm"; echo "bethere/$P" > "$FAKE/missing"; rebuild DOMAIN=x.example PREVIOUS_VERSION="$P"
check "typed rollback version not uploaded: stops" 'stopped "rollback version"'

fresh; rm "$FAKE/vm"; rebuild
check "no VM and nothing from paper: stops" 'stopped "set DOMAIN and PREVIOUS_VERSION"'
fresh; rm "$FAKE/vm"; rebuild DOMAIN=x.example
check "no VM and only DOMAIN: stops (the rollback version is asked for)" 'stopped "set DOMAIN and PREVIOUS_VERSION"'
fresh; rm "$FAKE/vm"; rebuild DOMAIN=https://x.example/ PREVIOUS_VERSION="$P"
check "bad DOMAIN typed: stops" 'stopped "not a host name"'
fresh; rm "$FAKE/vm"; rebuild DOMAIN=x..example PREVIOUS_VERSION="$P"
check "DOMAIN with an empty label: stops" 'stopped "not a host name"'
fresh; rm "$FAKE/vm"; rebuild DOMAIN=x.example PREVIOUS_VERSION="$P"
check "rerun with no VM: DOMAIN and PREVIOUS_VERSION from paper are used" \
  'create_has "bethere-version=$V,bethere-previous-version=$P,.*bethere-domain=x.example,"'
fresh; rm "$FAKE/vm"; rebuild DOMAIN=x.example PREVIOUS_VERSION=none
check "rerun with no VM and no rollback version: none is accepted" 'created && ! create_has "bethere-previous-version"'

fresh; echo junk > "$FAKE/meta.bethere-previous-version"; rebuild
check "VM with a hand-edited rollback record: rebuilt without one" 'created && ! create_has "bethere-previous-version"'
fresh; rm "$FAKE/meta.bethere-previous-version"; rebuild
check "VM without a rollback record: rebuilt without one" \
  'created && ! create_has "bethere-previous-version"'
fresh; rm "$FAKE/meta.bethere-previous-version"; echo down > "$FAKE/mode"; rebuild PROBE_SECONDS=1
check "slow VM: says to check from outside with curl, not to rerun" \
  'said "STOPPED at: wait for" && said "curl -s https://bethere.example/api/ping" && ! said "Run it again"'
fresh; rebuild DOMAIN=bethere.example PREVIOUS_VERSION="$P"
check "rerun with the printed values while the failed VM is there: goes on" 'deleted && created'

fresh; rebuild ZONE=asia-east1-a
check "ZONE left in the shell: ignored, the old VM's zone is used" 'create_has "--zone asia-east1-c"'
fresh; touch "$FAKE/failcreate"; rebuild
check "create fails: prints the whole rerun command" \
  'deleted && said "STOPPED at: create the new VM" && said "Run it again: PROJECT=test-proj DOMAIN=bethere.example PREVIOUS_VERSION=$P .*rebuild.sh $V asia-east1-c"'
ARGS=("$V" asia-east1-a); fresh; touch "$FAKE/failcreate"; rebuild
check "create fails in a chosen zone: the rerun command keeps that zone" 'said "rebuild.sh $V asia-east1-a"'
ARGS=("$V"); fresh; touch "$FAKE/faildelete"; rebuild
check "delete fails: prints the whole rerun command" 'said "STOPPED at: delete the old VM" && said "Run it again: PROJECT=test-proj .*rebuild.sh $V asia-east1-c"'
ARGS=("$V" asia-east1-a); fresh; rebuild
check "zone given: old VM deleted in its zone, new one in the given zone" \
  'called "instances delete bethere-1 --zone asia-east1-c" && create_has "--zone asia-east1-a"'

ARGS=("$V"); fresh; rebuild
check "rebuild: names the project and lists the VM it will delete" 'said "project: test-proj" && said "bethere-1 asia-east1-c RUNNING"'
check "rebuild: old VM deleted in its own zone, disks kept" 'called "instances delete bethere-1 --zone asia-east1-c --keep-disks=all"'
check "rebuild: new VM in the old VM's zone" 'create_has "--zone asia-east1-c"'
check "rebuild: new uniquely named disk" 'create_has "--create-disk boot=yes,name=bethere-1-[0-9]{14},"'
check "rebuild: domain, rollback version and maintenance carried over" \
  'create_has "bethere-version=$V,bethere-previous-version=$P,.*bethere-domain=bethere.example,bethere-maintenance=1"'
check "rebuild: checked from outside in maintenance mode, time reported" 'said "maintenance mode: https://bethere.example/" && said "rebuilt in [0-9]* min" && ! said STOPPED'

fresh; echo 1 > "$FAKE/sshfail"; rebuild
check "rebuild: waits until ssh reaches the new VM" 'said "rebuilt in" && ! said STOPPED && test "$(cat "$FAKE/sshfail")" = 0'
fresh; echo 99 > "$FAKE/sshfail"; rebuild PROBE_SECONDS=1
check "no ssh to the new VM: stops and says not to rebuild again" \
  'said "STOPPED at: wait for ssh" && said "do not rebuild again" && said "4003" && ! said "rebuilt in"'

ARGS=("$N"); fresh; rebuild
check "rebuild to another version: the old one becomes the rollback" 'create_has "bethere-version=$N,bethere-previous-version=$V,"'

fresh; echo "$N" > "$FAKE/deployed"; echo open > "$FAKE/mode"; run env PROJECT=test-proj "$OPS/deploy.sh" "$N"
check "deploy records the replaced version" 'called "bethere-version=$N,bethere-previous-version=$V "'
check "deploy follows the VM's zone" 'called "add-metadata bethere-1 --zone asia-east1-c"'
fresh; echo "bethere/def0000" > "$FAKE/missing"; run env PROJECT=test-proj "$OPS/deploy.sh" "$N"
check "deploy of a release not uploaded: refused, nothing changed" '! called add-metadata && said "missing gs://"'
fresh; run env PROJECT=test-proj "$OPS/deploy.sh"
check "deploy without a version lists, newest first, and changes nothing" \
  'said "previous: $P" && grep -A1 newest "$FAKE/out" | grep -q bbb-20261027000000 && ! called add-metadata'

# Email-only releases (US-CO-1.13): refused before anything changes.
fresh; echo "$N" > "$FAKE/emailonly"; run env PROJECT=test-proj "$OPS/deploy.sh" "$N"
check "deploy of an email-only release: refused, nothing changed" '! called add-metadata && said "email only"'
fresh; echo "features.txt" > "$FAKE/missing"; run env PROJECT=test-proj "$OPS/deploy.sh" "$N"
check "deploy of a release without features.txt: refused" '! called add-metadata && said "email only"'
ARGS=("$N"); fresh; echo "$N" > "$FAKE/emailonly"; rebuild
check "rebuild to an email-only release: stops before deleting" 'stopped "email only"'
fresh; echo "$P" > "$FAKE/emailonly"; run env PROJECT=test-proj "$OPS/deploy.sh"
check "deploy lists whether the running and previous releases may be deployed" \
  'said "running:  $V   (mail-login)" && said "previous: $P   (EMAIL-ONLY: cannot be deployed)"'

# Monitoring (US-O-7) and power.
mon() { run env PROJECT=test-proj "$@" "$OPS/monitoring.sh"; }
power() { run env PROJECT=test-proj "$@" "$OPS/power.sh" "$POWER"; }
alerts() { cat "$FAKE"/policies/*.enabled 2>/dev/null | tr '\n' ' '; }
policy() { cat "$(ls "$FAKE"/policies/p* | grep -v enabled)"; }
line_of() { grep -n -- "$1" "$FAKE/calls" | head -1 | cut -d: -f1; }

fresh; mon
check "monitoring without ALERT_EMAIL: refused, nothing made" 'said "set ALERT_EMAIL" && ! called monitoring'
fresh; mon ALERT_EMAIL='dev @example.com'
check "monitoring with a bad address: refused" 'said "not an email address" && ! called monitoring'
fresh; mon ALERT_EMAIL=dev@example.com; mon ALERT_EMAIL=dev@example.com
check "monitoring: checks the VM's domain and its certificate, mails dev, on while the VM runs" \
  'grep -q " bethere.example$" "$FAKE/check" && called "validate-ssl=true" && [ "$(alerts)" = "True " ] && said "alert: to dev@example.com, on"'
check "monitoring run twice: one check, one alert, one channel" \
  '[ "$(grep -c "uptime create" "$FAKE/calls")" = 1 ] && [ "$(grep -c "X POST" "$FAKE/calls")" = 1 ] && [ "$(alerts)" = "True " ]'
check "monitoring: the alert names the channel and the check" \
  'policy | grep -q "notificationChannels/77" && policy | grep -q "check_id=\\\\\"$(sed "s#.*/##; s/ .*//" "$FAKE/check")\\\\\""'
fresh; echo TERMINATED > "$FAKE/vm.status"; mon ALERT_EMAIL=dev@example.com
check "monitoring while the VM is off: the alert is made off" '[ "$(alerts)" = "False " ] && said "power.sh on"'
fresh; mon ALERT_EMAIL=dev@example.com; echo other.example > "$FAKE/meta.bethere-domain"; mon ALERT_EMAIL=dev@example.com
check "monitoring after the domain changed: check and alert remade for it" \
  'grep -q " other.example$" "$FAKE/check" && [ "$(alerts)" = "True " ] && policy | grep -q other.example'
fresh; mon ALERT_EMAIL=dev@example.com; echo other.example > "$FAKE/meta.bethere-domain"; touch "$FAKE/failcheck"; mon ALERT_EMAIL=dev@example.com
check "monitoring: new check fails after the old alert went: says to run it again" 'said "run monitoring.sh again"'
fresh; touch "$FAKE/nolist"; mon ALERT_EMAIL=dev@example.com
check "monitoring when listing fails: stops, makes nothing" '! called "uptime create" && ! called "policies create"'
fresh; touch "$FAKE/nochecklist"; mon ALERT_EMAIL=dev@example.com
check "monitoring when listing checks fails: makes no second check" '! called "uptime create" && ! called "policies create"'

fresh; mon ALERT_EMAIL=dev@example.com; POWER=off power
check "power off: alert off before the VM stops" \
  '[ "$(alerts)" = "False " ] && grep -q TERMINATED "$FAKE/vm.status" && [ "$(line_of "policies update")" -lt "$(line_of "instances stop")" ]'
POWER=on power
check "power on: VM started, alert on once the site answers" \
  'grep -q RUNNING "$FAKE/vm.status" && [ "$(alerts)" = "True " ] && said "on: https://bethere.example/"'
POWER=off power; echo down > "$FAKE/mode"; POWER=on power PROBE_SECONDS=1
check "power on, site never answers: says so, alert stays off" '[ "$(alerts)" = "False " ] && said "run power.sh on again once it answers"'
fresh; mon ALERT_EMAIL=dev@example.com; touch "$FAKE/nolist"; POWER=off power
check "power off when the alert cannot be read: the VM keeps running" '! called "instances stop"'
fresh; mon ALERT_EMAIL=dev@example.com; echo other.example > "$FAKE/meta.bethere-domain"; POWER=on power
check "power on when the check opens another domain: warns" 'said "the check opens bethere.example, not other.example"'
fresh; POWER=off power
check "power off with no alert yet: warns, still stops the VM" 'said "no alert yet" && grep -q TERMINATED "$FAKE/vm.status" && said "(no alert)"'
POWER=on power
check "power on with no alert yet: does not claim the alert is on" 'said "(no alert)" && ! said "alert on"'
fresh; mon ALERT_EMAIL=dev@example.com; POWER=status power
check "power status: VM and alert" 'said "VM: RUNNING" && said "alert enabled: True"'

# Support address (US-O-8.4).
support() { run env PROJECT=test-proj "$@" "$OPS/support-email.sh" "$ADDR"; }
ADDR='help me@example.com'; fresh; support
check "support address that is not one address: refused, nothing set" 'said "not one email address" && ! called project-info'
ADDR=help@example.com; fresh; support
check "support address set: in project metadata, no restart, says when it shows" \
  'grep -qx help@example.com "$FAKE/project.support" && ! called "compute ssh" && said "next start"'
ADDR=none; support
check "support address none: removed" '! test -e "$FAKE/project.support" && said "set to none"'

# The login mail's sender (ADR-0040).
mailset() { printf '%s\n' "$PW" | run env PROJECT=test-proj "$OPS/mail.sh" "$ADDR"; }
ADDR='a b@gmail.com'; PW=abcdabcdabcdabcd; fresh; mailset
check "mail sender that is not one address: refused, nothing set" 'said "not one email address" && ! called secrets && ! called project-info'
ADDR=bethere@gmail.com; PW=short; fresh; mailset
check "app password not 16 letters: refused, nothing set" 'said "16 lowercase letters" && ! called secrets && ! called project-info'
PW='abcd efgh ijkl mnop'; fresh; mailset
check "mail sender set: password in Secret Manager without spaces, address in metadata, no restart" \
  '[ "$(cat "$FAKE/secret")" = abcdefghijklmnop ] && grep -qx bethere@gmail.com "$FAKE/project.mailfrom" && called "secretmanager.secretAccessor" && ! called "compute ssh" && ! grep -q abcdefghijklmnop "$FAKE/calls"'
PW=qrstqrstqrstqrst; mailset
check "mail sender set again: a new version of the same secret" '[ "$(grep -c "secrets create" "$FAKE/calls")" = 1 ] && [ "$(cat "$FAKE/secret")" = qrstqrstqrstqrst ]'
ADDR=none; mailset
check "mail none: address and secret removed" '! test -e "$FAKE/project.mailfrom" && ! test -e "$FAKE/secret.exists" && said "stop"'

exit $failed
