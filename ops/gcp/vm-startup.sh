#!/usr/bin/env bash
# GCE startup script: runs on every boot. It only lays down users, swap and
# systemd units; installing the release happens in bethere-prepare, which runs
# each time the service starts (tech-stack.md section 3, "服務啟動").
set -euo pipefail

id bethere >/dev/null 2>&1 || useradd --system --home /var/lib/bethere --create-home --shell /usr/sbin/nologin bethere
id caddy >/dev/null 2>&1 || useradd --system --home /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy
mkdir -p /opt/bethere /etc/caddy

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
  caddy)
    v="$(md caddy-version)"; install_bin caddy "$v" "caddy/$v/caddy"
    printf '%s {\n\treverse_proxy 127.0.0.1:8080\n}\n' "$(md bethere-domain)" > /etc/caddy/Caddyfile ;;
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
ExecStart=/opt/bethere/bethere serve -addr 127.0.0.1:8080
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
