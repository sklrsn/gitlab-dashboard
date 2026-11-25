#!/usr/bin/env bash
# Install or upgrade gitlab-dashboard as a systemd service.
# Usage: sudo ./install.sh /path/to/gitlab-dashboard-linux-amd64
set -euo pipefail

BIN_SRC=${1:?usage: sudo $0 <path-to-binary>}
HERE=$(cd "$(dirname "$0")" && pwd)

[[ $EUID -eq 0 ]] || { echo "run as root (sudo)" >&2; exit 1; }
[[ -x $BIN_SRC ]] || chmod +x "$BIN_SRC"
"$BIN_SRC" version

if ! id gitlab-dashboard >/dev/null 2>&1; then
  useradd --system --no-create-home --home-dir /var/lib/gitlab-dashboard --shell /usr/sbin/nologin gitlab-dashboard
fi

install -m 0755 "$BIN_SRC" /usr/local/bin/gitlab-dashboard
install -m 0644 "$HERE/gitlab-dashboard.service" /etc/systemd/system/gitlab-dashboard.service
install -d -m 0750 -o root -g gitlab-dashboard /etc/gitlab-dashboard

if [[ ! -f /etc/gitlab-dashboard/env ]]; then
  install -m 0600 -o root -g root "$HERE/../env.example" /etc/gitlab-dashboard/env
  echo "Created /etc/gitlab-dashboard/env from the example."
  echo "Edit it (GITLAB_URL, GITLAB_TOKEN), then run: systemctl enable --now gitlab-dashboard"
  systemctl daemon-reload
  exit 0
fi

systemctl daemon-reload
systemctl enable gitlab-dashboard >/dev/null
systemctl restart gitlab-dashboard
sleep 1
systemctl --no-pager --lines=5 status gitlab-dashboard
