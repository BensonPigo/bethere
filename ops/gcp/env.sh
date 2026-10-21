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
CADDY_VERSION="2.11.7"
IAP_RANGE="35.235.240.0/20"

gc() { gcloud --project "$PROJECT" --quiet "$@"; }
