#!/usr/bin/env bash
# Usage: gws-reauth-install.sh <master|taidao> /path/to/exported-credentials.json
set -euo pipefail
tenant="${1:-}"
src="${2:-}"
case "$tenant" in
  master|taidao) ;;
  *) echo "usage: $0 <master|taidao> <credentials.json>"; exit 2 ;;
esac
test -f "$src" || { echo "missing file: $src"; exit 2; }
python3 - "$src" << 'PY'
import json,sys
d=json.load(open(sys.argv[1]))
need=[k for k in ("client_id","client_secret","refresh_token") if not d.get(k)]
if need:
    raise SystemExit(f"invalid credentials, missing: {need}")
print("credentials json ok")
PY

VOL=/var/lib/docker/volumes/goclaw-data/_data
dest_dir="$VOL/gws/${tenant}"
mkdir -p "$dest_dir/config"
install -m 600 -o 1000 -g 1000 "$src" "$dest_dir/credentials.json"
chown -R 1000:1000 "$VOL/gws/${tenant}"
chmod 700 "$dest_dir" "$dest_dir/config"

# also keep host mirror under /root/credentials for backup
install -m 600 "$src" "/root/credentials/gws-${tenant}.json"

# clear stale /tmp caches
docker exec goclaw-goclaw-1 sh -c "rm -rf /tmp/gws-${tenant} /tmp/gws-health-${tenant}" 2>/dev/null || true

echo "Installed $dest_dir/credentials.json (uid 1000). Env vars for SecureCLI:"
echo "  GOOGLE_WORKSPACE_CLI_CREDENTIALS_FILE=/app/data/gws/${tenant}/credentials.json"
echo "  GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file"
echo "  GOOGLE_WORKSPACE_CLI_CONFIG_DIR=/app/data/gws/${tenant}/config"
echo "Run health: /opt/deploy/goclaw/scripts/gws-health.sh"
