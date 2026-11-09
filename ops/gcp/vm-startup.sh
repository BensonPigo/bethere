#!/usr/bin/env bash
# GCE startup script: runs on every boot. It only lays down users, swap and
# systemd units; installing the release happens in bethere-prepare, which runs
# each time the service starts (tech-stack.md section 3, "服務啟動").
set -euo pipefail

id bethere >/dev/null 2>&1 || useradd --system --home /var/lib/bethere --create-home --shell /usr/sbin/nologin bethere
id caddy >/dev/null 2>&1 || useradd --system --home /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy
mkdir -p /opt/bethere /etc/caddy
# The test service's own directory (ADR-0029); the real database is never in it.
install -d -o bethere -g bethere -m 750 /var/lib/bethere-e2e

if [ ! -f /swapfile ]; then
  fallocate -l 1G /swapfile && chmod 600 /swapfile && mkswap /swapfile
  echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi
swapon --show | grep -q /swapfile || swapon /swapfile

cat > /opt/bethere/prepare.sh <<'EOF'
#!/usr/bin/env bash
# Install the release named in metadata, if it differs from what is installed.
set -euo pipefail
md() { curl -fsS -H "Metadata-Flavor: Google" "http://metadata.google.internal/computeMetadata/v1/instance/attributes/$1"; }
BUCKET="$(md release-bucket)"

install_bin() { # name version object
  local name="$1" version="$2" object="$3" marker="/opt/bethere/$1.installed"
  if [ -f "$marker" ] && [ "$(cat "$marker")" = "$version" ]; then return 0; fi
  gcloud storage cp --quiet "gs://$BUCKET/$object" "/opt/bethere/$name.new"
  chmod 755 "/opt/bethere/$name.new"
  mv -f "/opt/bethere/$name.new" "/opt/bethere/$name"
  echo "$version" > "$marker"
}

case "$1" in
  bethere) v="$(md bethere-version)"; install_bin bethere "$v" "bethere/$v/bethere" ;;
  # The real service only: Litestream, its config, and a restore when the
  # database is missing (tech-stack.md section 3, "服務啟動"). A failed restore
  # stops here; a restore that finds no backup leaves no file, and bethere
  # then refuses to start rather than open an empty database (ADR-0012).
  backup)
    v="$(md litestream-version)"; install_bin litestream "$v" "litestream/$v/litestream"
    bucket="$(md backup-bucket)"
    cat > /etc/litestream.yml.new <<YML
snapshot:
  interval: 24h
  retention: 72h
dbs:
  - path: /var/lib/bethere/bethere.db
    replica:
      url: gs://$bucket/bethere
YML
    mv -f /etc/litestream.yml.new /etc/litestream.yml
    db=/var/lib/bethere/bethere.db
    if [ ! -e "$db" ]; then
      # Leftovers of an earlier crash or interrupted restore; the .tmp may
      # hold a full copy of the data.
      rm -f "$db-wal" "$db-shm" "$db-journal" "$db.tmp"
      # A restored database opens in maintenance mode until the Master has
      # checked it (US-O-2). The marker is on disk before the restore, so a
      # crash after it can never start the restored data open. It is removed
      # by ops/gcp/maintenance.sh off, and by ops/gcp/init-db.sh for a new,
      # empty database. member-reset.pending makes serve clear every company
      # member's link of the backup before it takes a request; only serve
      # removes it, after the clearing worked (US-O-12.5).
      touch /var/lib/bethere/maintenance.forced /var/lib/bethere/member-reset.pending
      sync /var/lib/bethere/maintenance.forced /var/lib/bethere/member-reset.pending /var/lib/bethere
      runuser -u bethere -- /opt/bethere/litestream restore -config /etc/litestream.yml -if-replica-exists -integrity-check quick "$db"
      if [ -e "$db" ]; then chmod 600 "$db"; fi
    fi ;;
  # Maintenance mode (US-O-2.1): metadata bethere-maintenance 1 or the
  # restore marker. A missing key is 0; any other failure, or a value other
  # than exactly 0 or 1, stops the start rather than opening it. A release
  # that does not know the mode is refused while it is on.
  maintenance)
    # A refused start leaves no mode behind for maintenance.sh status.
    rm -f /run/bethere/maintenance.env
    body="$(mktemp)"
    code="$(curl -sS -o "$body" -w '%{http_code}' -H "Metadata-Flavor: Google" \
      "http://metadata.google.internal/computeMetadata/v1/instance/attributes/bethere-maintenance")"
    case "$code" in
      200) m="$(cat "$body")" ;;
      404) m=0 ;;
      *) echo "bethere-maintenance: metadata answered $code" >&2; exit 1 ;;
    esac
    rm -f "$body"
    if [ "$m" != 0 ] && [ "$m" != 1 ]; then
      echo "bethere-maintenance: $m is not 0 or 1" >&2; exit 1
    fi
    if [ -e /var/lib/bethere/maintenance.forced ]; then m=1; fi
    if [ "$m" = 1 ]; then
      features="$(runuser -u bethere -- /opt/bethere/bethere features)" || features=""
      if ! printf '%s\n' "$features" | grep -qx maintenance; then
        echo "the installed release does not know maintenance mode; refusing to start" >&2; exit 1
      fi
    fi
    mkdir -p /run/bethere
    echo "BETHERE_MAINTENANCE=$m" > /run/bethere/maintenance.env.new
    mv -f /run/bethere/maintenance.env.new /run/bethere/maintenance.env ;;
  # The support address (US-O-8.2): project metadata, shared by every VM.
  # It never stops the start: missing, unreadable or odd shows none. Only
  # address characters reach the unit's environment, so a value with a line
  # break cannot add another variable (BETHERE_MAINTENANCE above all); serve
  # checks the rest.
  support)
    e="$(curl -fsS -m 5 -H "Metadata-Flavor: Google" \
      "http://metadata.google.internal/computeMetadata/v1/project/attributes/bethere-support-email" 2>/dev/null || true)"
    [[ "$e" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$ ]] || e=""
    mkdir -p /run/bethere
    printf 'BETHERE_SUPPORT_EMAIL=%s\n' "$e" > /run/bethere/support.env ;;
  caddy)
    v="$(md caddy-version)"; install_bin caddy "$v" "caddy/$v/caddy"
    printf '%s {\n\treverse_proxy 127.0.0.1:8080\n}\n' "$(md bethere-domain)" > /etc/caddy/Caddyfile
    # The test service, once ops/gcp/e2e-setup.sh has named its domain (ADR-0029).
    e2e="$(md bethere-e2e-domain 2>/dev/null || true)"
    if [ -n "$e2e" ]; then
      printf '%s {\n\treverse_proxy 127.0.0.1:8081\n}\n' "$e2e" >> /etc/caddy/Caddyfile
    fi ;;
esac
EOF
chmod 755 /opt/bethere/prepare.sh

cat > /etc/systemd/system/bethere.service <<'EOF'
[Unit]
Description=BeThere
Wants=network-online.target
After=network-online.target

[Service]
User=bethere
ExecStartPre=+/opt/bethere/prepare.sh bethere
ExecStartPre=+/opt/bethere/prepare.sh backup
ExecStartPre=+/opt/bethere/prepare.sh maintenance
ExecStartPre=+/opt/bethere/prepare.sh support
# "unread" refuses to start; the file written by the step above replaces it.
Environment=BETHERE_MAINTENANCE=unread
# The support file is read first, so the maintenance flag always has the last word.
EnvironmentFile=-/run/bethere/support.env
EnvironmentFile=-/run/bethere/maintenance.env
# The database is never created here (tech-stack.md 2.1「不自動建庫」): the first
# deployment creates it once with ops/gcp/init-db.sh. Litestream copies every
# write to the backup bucket and stops when bethere stops.
ExecStart=/opt/bethere/litestream replicate -config /etc/litestream.yml -exec "/opt/bethere/bethere serve -db /var/lib/bethere/bethere.db -addr 127.0.0.1:8080"
Restart=on-failure
RestartSec=5
StartLimitIntervalSec=0
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

# The test service of the end-to-end routes (ADR-0029): the same release on
# its own database and port. It only runs once its database exists, which
# ops/gcp/e2e-setup.sh and every test run create; it never opens the real one.
cat > /etc/systemd/system/bethere-e2e.service <<'EOF'
[Unit]
Description=BeThere test service (end-to-end routes)
Wants=network-online.target
After=network-online.target bethere.service
ConditionPathExists=/var/lib/bethere-e2e/e2e.db

[Service]
User=bethere
ExecStartPre=+/opt/bethere/prepare.sh bethere
ExecStart=/opt/bethere/bethere serve -db /var/lib/bethere-e2e/e2e.db -addr 127.0.0.1:8081
Restart=on-failure
RestartSec=5
StartLimitIntervalSec=0
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/caddy.service <<'EOF'
[Unit]
Description=Caddy
Wants=network-online.target
After=network-online.target

[Service]
User=caddy
Environment=XDG_DATA_HOME=/var/lib/caddy XDG_CONFIG_HOME=/var/lib/caddy
ExecStartPre=+/opt/bethere/prepare.sh caddy
ExecStart=/opt/bethere/caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
Restart=on-failure
RestartSec=5
StartLimitIntervalSec=0
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now bethere.service caddy.service
systemctl enable bethere-e2e.service
