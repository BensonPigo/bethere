#!/usr/bin/env bash
# One-time project setup (tech-stack.md section 3): custom VPC, firewall,
# static IP, VM service account, release bucket. Safe to re-run.
set -euo pipefail
source "$(dirname "$0")/env.sh"

exists() { "$@" >/dev/null 2>&1; }

# The auto-created default network opens ssh/rdp to 0.0.0.0/0; remove it.
if exists gc compute networks describe default; then
  for rule in $(gc compute firewall-rules list --filter="network:default" --format="value(name)"); do
    gc compute firewall-rules delete "$rule"
  done
  gc compute networks delete default
fi

exists gc compute networks describe "$NETWORK" ||
  gc compute networks create "$NETWORK" --subnet-mode=custom
exists gc compute networks subnets describe "$SUBNET" --region "$REGION" ||
  gc compute networks subnets create "$SUBNET" --network "$NETWORK" --region "$REGION" --range 10.10.0.0/24

exists gc compute firewall-rules describe bethere-allow-web ||
  gc compute firewall-rules create bethere-allow-web --network "$NETWORK" \
    --allow tcp:80,tcp:443 --source-ranges 0.0.0.0/0 --target-tags bethere-web
exists gc compute firewall-rules describe bethere-allow-iap-ssh ||
  gc compute firewall-rules create bethere-allow-iap-ssh --network "$NETWORK" \
    --allow tcp:22 --source-ranges "$IAP_RANGE" --target-tags bethere-web

exists gc compute addresses describe "$ADDRESS" --region "$REGION" ||
  gc compute addresses create "$ADDRESS" --region "$REGION"

exists gc iam service-accounts describe "$SA_EMAIL" ||
  gc iam service-accounts create "$SA_NAME" --display-name "BeThere VM"

exists gc storage buckets describe "gs://$RELEASE_BUCKET" ||
  gc storage buckets create "gs://$RELEASE_BUCKET" --location "$REGION" \
    --uniform-bucket-level-access --public-access-prevention
# The VM may only read releases, never write them (tech-stack.md section 3).
gc storage buckets add-iam-policy-binding "gs://$RELEASE_BUCKET" \
  --member "serviceAccount:$SA_EMAIL" --role roles/storage.objectViewer >/dev/null

echo "static IP: $(gc compute addresses describe "$ADDRESS" --region "$REGION" --format='value(address)')"
