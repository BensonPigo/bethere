#!/usr/bin/env bash
# The login mail's sender (ADR-0040, tech-stack.md「寄信」). Usage:
#   mail.sh <gmail address>   then paste the app password when asked
#   mail.sh none              stops login mails
# The address goes into project metadata and the app password into Secret
# Manager, readable by the VM's service account; a rebuilt VM keeps both.
# The password is read from the terminal, never the command line. This
# script never restarts the service: the next start reads them
# (ops/gcp/deploy.sh <the running version>). It needs the project owner.
set -euo pipefail
source "$(dirname "$0")/env.sh"
KEY="bethere-mail-from"
SECRET="bethere-mail-password"

v="${1:?usage: mail.sh <gmail address>|none}"
if [ "$v" = none ]; then
  gc compute project-info remove-metadata --keys="$KEY" >/dev/null
  if gc secrets describe "$SECRET" >/dev/null 2>&1; then gc secrets delete "$SECRET" >/dev/null; fi
  echo "login mails stop from the service's next start: ops/gcp/deploy.sh <the running version>"
  exit 0
fi
# The same pattern as the start script's filter.
[[ "$v" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$ ]] || { echo "not one email address: $v" >&2; exit 2; }
printf 'app password for %s (16 letters, shown by Google once; not echoed): ' "$v" >&2
IFS= read -rs pw
echo >&2
pw="${pw// /}"
[[ "$pw" =~ ^[a-z]{16}$ ]] || { echo "an app password is 16 lowercase letters; nothing was changed" >&2; exit 2; }

gc services enable secretmanager.googleapis.com >/dev/null
if ! gc secrets describe "$SECRET" >/dev/null 2>&1; then
  gc secrets create "$SECRET" --replication-policy=automatic >/dev/null
fi
printf '%s' "$pw" | gc secrets versions add "$SECRET" --data-file=- >/dev/null
gc secrets add-iam-policy-binding "$SECRET" --member="serviceAccount:$SA_EMAIL" \
  --role=roles/secretmanager.secretAccessor >/dev/null
gc compute project-info add-metadata --metadata "$KEY=$v" >/dev/null
echo "set to $v; login mails go out from the service's next start: ops/gcp/deploy.sh <the running version>, then send one to yourself at /login"
