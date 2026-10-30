#!/usr/bin/env bash
set -euo pipefail
fail=0
probe() {
  local name="$1" cred="$2" cfg="$3"
  if ! docker exec -u goclaw goclaw-goclaw-1 test -f "$cred" 2>/dev/null; then
    echo "[gws-health] FAIL tenant=$name reason=missing_creds path=$cred"
    fail=1
    return
  fi
  if docker exec -u goclaw \
      -e GOOGLE_WORKSPACE_CLI_CREDENTIALS_FILE="$cred" \
      -e GOOGLE_WORKSPACE_CLI_CONFIG_DIR="$cfg" \
      -e GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file \
      goclaw-goclaw-1 gws drive about get --params '{"fields":"user"}' >/tmp/gws-health-$name.out 2>/tmp/gws-health-$name.err; then
    email=$(python3 -c "import json;print(json.load(open('/tmp/gws-health-$name.out')).get('user',{}).get('emailAddress',''))" 2>/dev/null || true)
    echo "[gws-health] OK tenant=$name email=$email"
  else
    echo "[gws-health] FAIL tenant=$name"
    tail -c 300 /tmp/gws-health-$name.err 2>/dev/null || true
    fail=1
  fi
}
probe master /app/data/gws/master/credentials.json /app/data/gws/master/config
probe taidao /app/data/gws/taidao/credentials.json /app/data/gws/taidao/config
exit $fail
