# gws re-auth runbook

## When
gws-health FAIL / agent reports invalid_grant / exit code 2.

## Steps (operator laptop with browser)

1. Install gws locally: `npm i -g @googleworkspace/cli@0.22.5` or download release binary.
2. Place Desktop OAuth client_secret.json (same GCP project as Production app).
3. Per account:

```bash
export GOOGLE_WORKSPACE_CLI_CONFIG_DIR=/tmp/gws-auth-master
mkdir -p "$GOOGLE_WORKSPACE_CLI_CONFIG_DIR"
cp client_secret.json "$GOOGLE_WORKSPACE_CLI_CONFIG_DIR/"
gws auth login -s drive,sheets,docs,calendar
gws auth export --unmasked > /tmp/gws-master.json
```

Repeat with CONFIG_DIR=/tmp/gws-auth-taidao for TaiDao Google account.

4. SCP to VPS:
```bash
scp /tmp/gws-master.json root@VPS:/tmp/gws-master.json
scp /tmp/gws-taidao.json root@VPS:/tmp/gws-taidao.json
```

5. On VPS:
```bash
/opt/deploy/goclaw/scripts/gws-reauth-install.sh master /tmp/gws-master.json
/opt/deploy/goclaw/scripts/gws-reauth-install.sh taidao /tmp/gws-taidao.json
/opt/deploy/goclaw/scripts/gws-health.sh
```

6. No goclaw restart needed if path unchanged.

## Production OAuth
Consent screen must be **In production** so refresh tokens do not die every ~7 days (Testing mode).
