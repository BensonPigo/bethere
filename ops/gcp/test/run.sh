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


# ---- the VM's prepare script (US-O-1, 2, 8), in a scratch directory.
# The script is cut out of vm-startup.sh as it is (the heredoc) and only its
# fixed paths are pointed into $ROOT; litestream, the release binary, curl,
# runuser and gcloud are fakes. Nothing of the real scripts is changed.
setmd() { printf '%s' "$2" > "$FAKE/md/${1//\//_}"; } # path value
runrc() { run "$@"; echo $? > "$FAKE/rc"; }
rc_is() { [ "$(cat "$FAKE/rc")" = "$1" ]; }
pfresh() {
  fresh
  ROOT="$FAKE/root"; export ROOT
  mkdir -p "$ROOT/opt/bethere" "$ROOT/var/lib/bethere" "$ROOT/etc/caddy" "$ROOT/run" "$FAKE/md"
  awk '/^cat > \/opt\/bethere\/prepare.sh <<.EOF.$/{on=1;next} on&&/^EOF$/{exit} on' "$OPS/vm-startup.sh" |
    sed "s#/var/lib/bethere#$ROOT/var/lib/bethere#g; s#/opt/bethere#$ROOT/opt/bethere#g; s#/etc/litestream.yml#$ROOT/etc/litestream.yml#g; s#/etc/caddy#$ROOT/etc/caddy#g; s#/run/bethere#$ROOT/run/bethere#g" > "$ROOT/prepare.sh"
  cat > "$ROOT/opt/bethere/litestream" <<'EOF'
#!/usr/bin/env bash
# Records what the disk looked like when the restore was asked for.
db="${@: -1}"
{ echo "args: $*"
  for f in maintenance.forced member-reset.pending; do [ -e "$ROOT/var/lib/bethere/$f" ] && echo "has $f"; done
  for s in -wal -shm -journal .tmp; do [ -e "$db$s" ] && echo "has $s"; done; } >> "$FAKE/ls.log"
case "$(cat "$FAKE/ls.mode" 2>/dev/null || echo ok)" in
  ok) echo restored > "$db"; chmod 644 "$db" ;;
  none) ;;
  *) echo "restore failed" >&2; exit 1 ;;
esac
EOF
  cat > "$ROOT/opt/bethere/bethere" <<'EOF'
#!/usr/bin/env bash
[ "$1" = features ] && cat "$FAKE/features"
exit 0
EOF
  chmod +x "$ROOT/opt/bethere/litestream" "$ROOT/opt/bethere/bethere"
  printf 'maintenance\nmail-login\n' > "$FAKE/features"
  : > "$FAKE/ls.log"
  setmd instance/attributes/release-bucket rel
  setmd instance/attributes/litestream-version 0.5.17
  setmd instance/attributes/backup-bucket test-backups
  echo 0.5.17 > "$ROOT/opt/bethere/litestream.installed" # already installed: no gcloud
}
prep() { runrc bash "$ROOT/prepare.sh" "$@"; }
ls_log_has() { grep -qx -- "$1" "$FAKE/ls.log"; }
mode_600() { [ "$(ls -l "$1" | cut -c1-10)" = "-rw-------" ]; }

# US-O-1.2: no database file: the leftovers of a crash go first, the restore
# markers are on disk before the restore starts, and the file comes out 0600.
pfresh; touch "$ROOT/var/lib/bethere/bethere.db-wal" "$ROOT/var/lib/bethere/bethere.db-shm" \
  "$ROOT/var/lib/bethere/bethere.db-journal" "$ROOT/var/lib/bethere/bethere.db.tmp"; prep backup
check "prepare backup, database missing: -wal, -shm, -journal and .tmp are gone before the restore" \
  'rc_is 0 && ! ls_log_has "has -wal" && ! ls_log_has "has -shm" && ! ls_log_has "has -journal" && ! ls_log_has "has .tmp" && [ "$(grep -c "^args:" "$FAKE/ls.log")" = 1 ] && [ ! -e "$ROOT/var/lib/bethere/bethere.db-wal" ] && [ ! -e "$ROOT/var/lib/bethere/bethere.db.tmp" ]'
check "prepare backup: both markers exist when the restore starts (US-O-1.2, 2.6)" \
  'ls_log_has "has maintenance.forced" && ls_log_has "has member-reset.pending"'
check "prepare backup: restore with the replica-exists and integrity flags on the database" \
  'grep -qx -- "args: restore -config $ROOT/etc/litestream.yml -if-replica-exists -integrity-check quick $ROOT/var/lib/bethere/bethere.db" "$FAKE/ls.log"'
check "prepare backup: the restored database is 0600 (US-O-1.7)" 'mode_600 "$ROOT/var/lib/bethere/bethere.db"'
check "prepare backup: markers stay after a good restore (only maintenance.sh off and serve remove them)" \
  '[ -e "$ROOT/var/lib/bethere/maintenance.forced" ] && [ -e "$ROOT/var/lib/bethere/member-reset.pending" ]'

pfresh; echo fail > "$FAKE/ls.mode"; prep backup
check "prepare backup, restore fails: the start stops, no database, marker stays on" \
  '! rc_is 0 && [ ! -e "$ROOT/var/lib/bethere/bethere.db" ] && [ -e "$ROOT/var/lib/bethere/maintenance.forced" ] && said "restore failed"'
pfresh; echo none > "$FAKE/ls.mode"; prep backup
check "prepare backup, no replica: no file is made (serve then refuses), marker stays on" \
  'rc_is 0 && [ ! -e "$ROOT/var/lib/bethere/bethere.db" ] && [ -e "$ROOT/var/lib/bethere/maintenance.forced" ]'
pfresh; echo kept > "$ROOT/var/lib/bethere/bethere.db"; echo wal > "$ROOT/var/lib/bethere/bethere.db-wal"; prep backup
check "prepare backup, database present: no restore, nothing removed, no marker" \
  'rc_is 0 && [ ! -s "$FAKE/ls.log" ] && [ "$(cat "$ROOT/var/lib/bethere/bethere.db")" = kept ] && [ -e "$ROOT/var/lib/bethere/bethere.db-wal" ] && [ ! -e "$ROOT/var/lib/bethere/maintenance.forced" ] && [ ! -e "$ROOT/var/lib/bethere/member-reset.pending" ]'

# US-O-1.5, 1.6: the Litestream settings: daily snapshots kept 72 hours,
# the real database only, never the test service's.
pfresh; prep backup
check "litestream.yml: snapshot every 24h, kept 72h" \
  'grep -qx "snapshot:" "$ROOT/etc/litestream.yml" && grep -qx "  interval: 24h" "$ROOT/etc/litestream.yml" && grep -qx "  retention: 72h" "$ROOT/etc/litestream.yml"'
check "litestream.yml: only the real database, to the backup bucket" \
  '[ "$(grep -c "^  - path:" "$ROOT/etc/litestream.yml")" = 1 ] && grep -qx "  - path: $ROOT/var/lib/bethere/bethere.db" "$ROOT/etc/litestream.yml" && grep -qx "      url: gs://test-backups/bethere" "$ROOT/etc/litestream.yml" && ! grep -q "e2e" "$ROOT/etc/litestream.yml" && [ ! -e "$ROOT/etc/litestream.yml.new" ]'
e2e_unit() { awk "/bethere-e2e.service <<.EOF./{on=1;next} on&&/^EOF\$/{exit} on" "$OPS/vm-startup.sh"; }
check "systemd units: the real service runs under litestream replicate -exec, the test service never does" \
  'grep -q "^ExecStart=/opt/bethere/litestream replicate -config /etc/litestream.yml -exec \"/opt/bethere/bethere serve -db /var/lib/bethere/bethere.db " "$OPS/vm-startup.sh" && ! e2e_unit | grep -q litestream && e2e_unit | grep -q "^ExecStart=/opt/bethere/bethere serve -db /var/lib/bethere-e2e/e2e.db"'
check "the settings are in the tech-stack document too (US-O-1.5)" \
  'grep -q "Litestream 快照間隔 24h、保留 72h" "$OPS/../../docs/design/tech-stack.md"'

# US-O-2.1: maintenance mode from metadata and the restore marker.
mflag() { cat "$ROOT/run/bethere/maintenance.env" 2>/dev/null; }
pfresh; prep maintenance
check "maintenance: key missing (404) is 0" 'rc_is 0 && [ "$(mflag)" = BETHERE_MAINTENANCE=0 ]'
pfresh; setmd instance/attributes/bethere-maintenance 0; prep maintenance
check "maintenance: 0 is 0" 'rc_is 0 && [ "$(mflag)" = BETHERE_MAINTENANCE=0 ]'
pfresh; setmd instance/attributes/bethere-maintenance 1; prep maintenance
check "maintenance: 1 is 1 when the release knows the mode" 'rc_is 0 && [ "$(mflag)" = BETHERE_MAINTENANCE=1 ]'
pfresh; setmd instance/attributes/bethere-maintenance 1; echo mail-login > "$FAKE/features"; prep maintenance
check "maintenance: 1 with a release that does not know the mode: refused, no file" \
  '! rc_is 0 && [ ! -e "$ROOT/run/bethere/maintenance.env" ] && said "does not know maintenance mode"'
for bad in 2 "" true " 1" 01 "1 1"; do
  pfresh; setmd instance/attributes/bethere-maintenance "$bad"; prep maintenance
  check "maintenance: value '$bad' is neither 0 nor 1: refused, not opened" \
    '! rc_is 0 && [ ! -e "$ROOT/run/bethere/maintenance.env" ] && said "is not 0 or 1"'
done
for code in 500 403 302; do
  pfresh; echo "$code" > "$FAKE/mdcode.instance_attributes_bethere-maintenance"; prep maintenance
  check "maintenance: metadata answers $code: refused, not opened" \
    '! rc_is 0 && [ ! -e "$ROOT/run/bethere/maintenance.env" ] && said "metadata answered '"$code"'"'
done
pfresh; echo fail > "$FAKE/mdcode.instance_attributes_bethere-maintenance"; prep maintenance
check "maintenance: metadata unreachable: refused, never defaults to open" '! rc_is 0 && [ ! -e "$ROOT/run/bethere/maintenance.env" ]'
pfresh; touch "$ROOT/var/lib/bethere/maintenance.forced"; setmd instance/attributes/bethere-maintenance 0; prep maintenance
check "maintenance: the restore marker forces 1 over metadata 0" 'rc_is 0 && [ "$(mflag)" = BETHERE_MAINTENANCE=1 ]'
pfresh; touch "$ROOT/var/lib/bethere/maintenance.forced"; prep maintenance
check "maintenance: the restore marker forces 1 when the key is missing" 'rc_is 0 && [ "$(mflag)" = BETHERE_MAINTENANCE=1 ]'
pfresh; touch "$ROOT/var/lib/bethere/maintenance.forced"; echo mail-login > "$FAKE/features"; prep maintenance
check "maintenance: the marker with a release that does not know the mode: refused" '! rc_is 0 && [ ! -e "$ROOT/run/bethere/maintenance.env" ]'
pfresh; mkdir -p "$ROOT/run/bethere"; echo BETHERE_MAINTENANCE=0 > "$ROOT/run/bethere/maintenance.env"; setmd instance/attributes/bethere-maintenance 2; prep maintenance
check "maintenance: a refused start leaves no earlier mode behind" '! rc_is 0 && [ ! -e "$ROOT/run/bethere/maintenance.env" ]'

# US-O-8.2: the support address never adds a variable to the unit.
sfile() { cat "$ROOT/run/bethere/support.env"; }
pfresh; setmd project/attributes/bethere-support-email help@example.com; prep support
check "support: a plain address is passed on" 'rc_is 0 && [ "$(sfile)" = BETHERE_SUPPORT_EMAIL=help@example.com ]'
pfresh; setmd project/attributes/bethere-support-email "$(printf 'a@b.com\nBETHERE_MAINTENANCE=0')"; prep support
check "support: an address with a line break and a variable: none shown, one line, no second variable" \
  'rc_is 0 && [ "$(sfile)" = BETHERE_SUPPORT_EMAIL= ] && [ "$(wc -l < "$ROOT/run/bethere/support.env" | tr -d " ")" = 1 ] && ! grep -q MAINTENANCE "$ROOT/run/bethere/support.env"'
pfresh; setmd project/attributes/bethere-support-email "$(printf 'BETHERE_MAINTENANCE=0\nhelp@example.com')"; prep support
check "support: the variable on the first line: none shown, no second variable" \
  'rc_is 0 && [ "$(sfile)" = BETHERE_SUPPORT_EMAIL= ]'
pfresh; setmd project/attributes/bethere-support-email 'help@example.com X=1'; prep support
check "support: an address with a space: none shown" '[ "$(sfile)" = BETHERE_SUPPORT_EMAIL= ]'
pfresh; prep support
check "support: no address (404): none shown, the start goes on" 'rc_is 0 && [ "$(sfile)" = BETHERE_SUPPORT_EMAIL= ]'
pfresh; echo 500 > "$FAKE/mdcode.project_attributes_bethere-support-email"; prep support
check "support: metadata error: none shown, the start goes on" 'rc_is 0 && [ "$(sfile)" = BETHERE_SUPPORT_EMAIL= ]'

# ---- maintenance.sh (US-O-2.6, 12.5, 12.6) against a fake VM's files.
vmfs() { mkdir -p "$FAKE/vm.fs/lib" "$FAKE/vm.fs/run"; echo "${1:-1}" > "$FAKE/meta.bethere-maintenance"; }
msh() { runrc env PROJECT=test-proj "$OPS/maintenance.sh" "$@"; }
fresh; vmfs 1; touch "$FAKE/vm.fs/lib/maintenance.forced" "$FAKE/vm.fs/lib/member-reset.pending"; echo open > "$FAKE/mode"; msh off
check "maintenance.sh off: metadata 0, restore marker removed, service restarted, checked from outside" \
  'rc_is 0 && called "add-metadata bethere-1 --zone asia-east1-c --metadata bethere-maintenance=0" && [ ! -e "$FAKE/vm.fs/lib/maintenance.forced" ] && called "systemctl restart bethere" && said "BETHERE_MAINTENANCE=0" && said "marker=absent" && said "open: https://bethere.example/"'
check "maintenance.sh off: member-reset.pending stays, status says the members are not cleared yet (US-O-12.5, 12.6)" \
  '[ -e "$FAKE/vm.fs/lib/member-reset.pending" ] && said "member_reset=pending" && said "成員連結尚未清除"'
fresh; vmfs 0; touch "$FAKE/vm.fs/lib/maintenance.forced"; msh on
check "maintenance.sh on: metadata 1, the marker is not touched, restarted, in maintenance mode" \
  'rc_is 0 && called "--metadata bethere-maintenance=1" && [ -e "$FAKE/vm.fs/lib/maintenance.forced" ] && called "systemctl restart bethere" && said "BETHERE_MAINTENANCE=1" && said "marker=present" && said "maintenance mode: https://bethere.example/" && ! said "成員連結尚未清除"'
fresh; vmfs 1; touch "$FAKE/vm.fs/lib/maintenance.forced" "$FAKE/vm.fs/lib/member-reset.pending"; echo BETHERE_MAINTENANCE=1 > "$FAKE/vm.fs/run/maintenance.env"; msh status
check "maintenance.sh status: shows metadata, marker and the pending member clearing; changes nothing" \
  'rc_is 0 && said "metadata bethere-maintenance=1" && said "marker=present" && said "member_reset=pending" && said "成員連結尚未清除" && ! called add-metadata && ! called "systemctl restart" && [ -e "$FAKE/vm.fs/lib/maintenance.forced" ]'
fresh; vmfs 0; echo BETHERE_MAINTENANCE=1 > "$FAKE/vm.fs/run/maintenance.env"; msh status
check "maintenance.sh status: nothing pending says nothing about members" 'rc_is 0 && said "marker=absent" && ! said "member_reset" && ! said "成員連結尚未清除"'
fresh; vmfs 1; msh status
check "maintenance.sh status: a service that has not started since boot is an error" '! rc_is 0 && said "has not started since boot"'
fresh; vmfs 1; msh bogus
check "maintenance.sh: an unknown word is refused with usage, nothing called on the VM" 'rc_is 2 && said "usage: maintenance.sh on|off|status" && ! called add-metadata && ! called "compute ssh"'
fresh; vmfs 1; msh
check "maintenance.sh: no word is refused with usage" 'rc_is 2 && said "usage: maintenance.sh on|off|status"'

# US-O-6.4: every script that acts on a project refuses without PROJECT and
# calls nothing (env.sh itself and the VM's startup script excepted).
for script in "$OPS"/*.sh; do
  name="${script##*/}"
  case "$name" in env.sh | vm-startup.sh) continue ;; esac
  fresh; runrc env -u PROJECT "$script" </dev/null
  check "$name without PROJECT: refused, nothing called" '! rc_is 0 && ! test -s "$FAKE/calls" && said "set PROJECT first"'
done

# US-O-1.4: the backup bucket: same region, no soft delete, no versions, no
# lifecycle rules; the VM's account may only add and read objects there.
BK=gs://test-proj-backups RL=gs://test-proj-releases SA=serviceAccount:bethere-vm@test-proj.iam.gserviceaccount.com
fresh; touch "$FAKE/nobucket"; runrc env PROJECT=test-proj "$OPS/setup.sh"
check "setup: the backup bucket is made in the region with soft delete off" \
  'rc_is 0 && grep "buckets create $BK " "$FAKE/calls" | grep -q -- "--location asia-east1" && grep "buckets create $BK " "$FAKE/calls" | grep -q -- "--soft-delete-duration 0"'
check "setup: soft delete, versioning and lifecycle rules of the backup bucket are cleared" \
  'grep "buckets update $BK " "$FAKE/calls" | grep -q -- "--clear-soft-delete" && grep "buckets update $BK " "$FAKE/calls" | grep -q -- "--no-versioning" && grep "buckets update $BK " "$FAKE/calls" | grep -q -- "--clear-lifecycle" && ! grep -q -- " --versioning" "$FAKE/calls" && ! grep -q -- "--lifecycle-file" "$FAKE/calls"'
check "setup: the release bucket is in the same region" 'grep "buckets create $RL " "$FAKE/calls" | grep -q -- "--location asia-east1"'
check "setup: the VM account is objectAdmin of the backups and only objectViewer of the releases" \
  'grep "add-iam-policy-binding $BK " "$FAKE/calls" | grep -q -- "--member $SA --role roles/storage.objectAdmin" && grep "add-iam-policy-binding $RL " "$FAKE/calls" | grep -q -- "--member $SA --role roles/storage.objectViewer" && [ "$(grep -c add-iam-policy-binding "$FAKE/calls")" = 2 ]'
fresh; runrc env PROJECT=test-proj "$OPS/setup.sh"
check "setup run again: no bucket is made again, the backup bucket's settings are applied again" \
  'rc_is 0 && ! called "buckets create" && grep -q "buckets update $BK .*--clear-soft-delete.*--no-versioning.*--clear-lifecycle" "$FAKE/calls"'

# US-O-13.1: the load test's data never goes on the real project.
fresh; runrc env PROJECT=real-proj PROD_PROJECT=real-proj "$OPS/loadtest-setup.sh"
check "loadtest-setup on the real project: refused before anything runs on a VM" 'rc_is 2 && said "refusing: the load test never runs on the real project" && ! called "compute ssh"'
fresh; runrc env PROJECT=drill-proj PROD_PROJECT=real-proj "$OPS/loadtest-setup.sh"
check "loadtest-setup on another project: goes on to the VM" 'rc_is 0 && ! said "refusing" && called "compute ssh"'
fresh; runrc env PROJECT=drill-proj PROD_PROJECT= "$OPS/loadtest-setup.sh"
check "loadtest-setup with the real project unknown: refused before anything runs on a VM" 'rc_is 2 && said "refusing: the real project is unknown" && ! called "compute ssh"'

# US-O-7.1: the uptime check and the alert's settings.
policy_has() { policy | grep -qF -- "$1"; }
fresh; mon ALERT_EMAIL=dev@example.com
check "monitoring: the check opens /healthz every minute, 10 s timeout, from outside" \
  'grep "uptime create" "$FAKE/calls" | grep -q -- "--path=/healthz" && grep "uptime create" "$FAKE/calls" | grep -q -- "--period=1 " && grep "uptime create" "$FAKE/calls" | grep -q -- "--timeout=10 "'
check "monitoring: the alert fires after 2 minutes of failures, counted per minute" \
  'policy_has "\"duration\": \"120s\"" && policy_has "\"alignmentPeriod\": \"60s\"" && policy_has "\"thresholdValue\": 1" && policy_has "COMPARISON_GT" && policy_has "REDUCE_COUNT_FALSE"'
check "monitoring: mail when it opens and when it recovers" 'policy_has "\"notificationPrompts\": [\"OPENED\", \"CLOSED\"]" && policy_has "\"autoClose\": \"1800s\"" && policy_has "notificationChannels/77"'

# US-O-6.2: a rebuild deletes every VM there is (disks kept), makes exactly
# one, and puts it on the static IP.
ARGS=("$V"); fresh; printf 'bethere-1 asia-east1-c\nbethere-2 asia-east1-a\n' > "$FAKE/vms"; rebuild
check "rebuild with two VMs: both deleted in their own zones, disks kept" \
  'called "instances delete bethere-1 --zone asia-east1-c --keep-disks=all" && called "instances delete bethere-2 --zone asia-east1-a --keep-disks=all" && said "bethere-2 asia-east1-a"'
check "rebuild with two VMs: exactly one is created, after the deletions, on the existing static IP" \
  '[ "$(grep -c "instances create" "$FAKE/calls")" = 1 ] && [ "$(line_of "instances delete bethere-2")" -lt "$(line_of "instances create")" ] && create_has "--address 10.0.0.1" && ! called "addresses create" && ! called "addresses delete" && said "rebuilt in"'

exit $failed
