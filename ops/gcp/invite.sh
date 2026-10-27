#!/usr/bin/env bash
# Make one invite code for /new on the real database (US-M-17.1, ADR-0036).
# The code is printed here once, with its number; only its hash is kept.
# The service keeps running: the command takes the write lock briefly.
set -euo pipefail
source "$(dirname "$0")/env.sh"

vm_ssh 'sudo -u bethere /opt/bethere/bethere create-invite -db /var/lib/bethere/bethere.db'
