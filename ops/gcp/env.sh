# Shared settings for the GCP scripts. Source this file; do not run it.
# The project is never assumed: a drill that forgot to name its own would
# act on the real one (RISK-4).
if [ -z "${PROJECT:-}" ]; then
  echo "set PROJECT first: export PROJECT=bethere-prod-0606 (the real one) or the drill project" >&2
  exit 2
fi
echo "project: $PROJECT" >&2
REGION="asia-east1"
NETWORK="bethere-vpc"
SUBNET="bethere-asia-east1"
ADDRESS="bethere-ip"
VM="${VM:-bethere-1}"
SA_NAME="bethere-vm"
SA_EMAIL="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"
RELEASE_BUCKET="${PROJECT}-releases"
BACKUP_BUCKET="${PROJECT}-backups"
CADDY_VERSION="2.11.7"
LITESTREAM_VERSION="0.5.17"
IAP_RANGE="35.235.240.0/20"

gc() { gcloud --project "$PROJECT" --quiet "$@"; }

# ZONE, unless given, is where the VM is, so every script follows a VM
# rebuilt in another zone; with no VM it is the zone a new one goes to.
if [ -z "${ZONE:-}" ]; then
  ZONE="$(gc compute instances list --filter="name=$VM" --format='value(zone.basename())' 2>/dev/null | head -1 || true)"
  ZONE="${ZONE:-asia-east1-b}"
fi

version_ok() { [[ "$1" =~ ^[0-9a-f]+-[0-9]{14}$ ]]; }
# domain_ok: a host name people open, without scheme or slash, e.g. 34-80-7-111.sslip.io.
domain_ok() { [[ "$1" =~ ^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$ ]]; }

# check_release refuses a version that is malformed or not in the release
# bucket, together with the Caddy and Litestream builds a VM downloads at
# boot, before anything on the VM changes; and an email-only release, whose
# features.txt lacks mail-login or is missing (M4 and before; US-CO-1.13,
# tech-stack.md「寄信」). The VM checks the installed release again.
check_release() {
  if ! version_ok "$1"; then
    echo "not a release version: $1 (e.g. c406027-20261027032903)" >&2
    return 1
  fi
  local object
  for object in "bethere/$1/bethere" "caddy/$CADDY_VERSION/caddy" "litestream/$LITESTREAM_VERSION/litestream"; do
    gc storage objects describe "gs://$RELEASE_BUCKET/$object" >/dev/null ||
      { echo "missing gs://$RELEASE_BUCKET/$object (ops/gcp/release.sh uploads it)" >&2; return 1; }
  done
  mail_login "$1" ||
    { echo "$1 lets members in with an email only (no mail-login in its features.txt); it is never deployed" >&2; return 1; }
}

# mail_login: the release's features.txt lists mail-login.
mail_login() { gc storage cat "gs://$RELEASE_BUCKET/bethere/$1/features.txt" 2>/dev/null | grep -qx mail-login; }

# vm_ssh runs a command on the VM; its errors stay visible, the IAP
# tunnel's performance warnings do not.
vm_ssh() {
  gc compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command "$1" \
    2> >(grep -v -i -e numpy -e 'tcp_upload_bandwidth' -e '^WARNING:' -e '^$' >&2)
}

# probe_mode checks from outside that the site answers in mode $1 (0 open,
# 1 maintenance): GET /api/ping is 200 when open and 503 "maintenance"
# when not. It waits up to PROBE_SECONDS (a minute) of wall clock.
probe_mode() {
  local want="$1" domain code body
  domain="$(gc compute instances describe "$VM" --zone "$ZONE" \
    --format='value(metadata.items.bethere-domain)')"
  local wait="${PROBE_SECONDS:-60}"
  [[ "$wait" =~ ^[1-9][0-9]*$ ]] || { echo "PROBE_SECONDS must be a number of seconds" >&2; return 1; }
  body="$(mktemp)"
  local deadline=$((SECONDS + wait))
  while [ "$SECONDS" -lt "$deadline" ]; do
    code="$(curl -s -m 5 -o "$body" -w '%{http_code}' "https://$domain/api/ping" || true)"
    if [ "$want" = 0 ] && [ "$code" = 200 ]; then rm -f "$body"; echo "open: https://$domain/"; return 0; fi
    if [ "$want" = 1 ] && [ "$code" = 503 ] && grep -q '"maintenance"' "$body"; then
      rm -f "$body"; echo "maintenance mode: https://$domain/"; return 0
    fi
    sleep 2
  done
  echo "https://$domain/api/ping answers $code, not $([ "$want" = 1 ] && echo 'maintenance' || echo 'open')" >&2
  rm -f "$body"
  return 1
}
