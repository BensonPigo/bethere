# Shared settings for the GCP scripts. Source this file; do not run it.
PROJECT="${PROJECT:-bethere-prod-0606}"
REGION="asia-east1"
ZONE="${ZONE:-asia-east1-b}"
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

# vm_ssh runs a command on the VM; its errors stay visible, the IAP
# tunnel's performance warnings do not.
vm_ssh() {
  gc compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command "$1" \
    2> >(grep -v -i -e numpy -e 'tcp_upload_bandwidth' -e '^WARNING:' -e '^$' >&2)
}

# probe_mode checks from outside that the site answers in mode $1 (0 open,
# 1 maintenance): GET /api/ping is 200 when open and 503 "maintenance"
# when not. It waits up to a minute for the restart to finish.
probe_mode() {
  local want="$1" domain code body
  domain="$(gc compute instances describe "$VM" --zone "$ZONE" \
    --format='value(metadata.items.bethere-domain)')"
  body="$(mktemp)"
  for _ in $(seq 1 30); do
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
