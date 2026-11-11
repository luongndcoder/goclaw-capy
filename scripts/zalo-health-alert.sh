#!/usr/bin/env bash
# scripts/zalo-health-alert.sh — Stateful Zalo Personal health monitor & Telegram alerter.
# Queries GoClaw's official WebSocket RPC endpoint (channels.status).
# Only fires Telegram alerts on state transitions (OK -> ERROR or ERROR -> RECOVERED)
# to prevent spamming when run periodically (e.g. cron every 5 minutes).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_FILE="${ZALO_HEALTH_STATE_FILE:-/tmp/goclaw-zalo-health.state}"
ENV_FILE="${GOCLAW_ENV_FILE:-/opt/deploy/goclaw/.env}"
CHANNELS="${ZALO_CHANNELS:-bao-su-de,zalo-bot}"

# Parse command line flags
DRY_RUN=0
TEST_MODE=0
PRINT_STATUS=0
RESET_STATE=0

for arg in "$@"; do
  case "$arg" in
    --dry-run)
      DRY_RUN=1
      ;;
    --test)
      TEST_MODE=1
      ;;
    --status)
      PRINT_STATUS=1
      ;;
    --reset-state)
      RESET_STATE=1
      ;;
    --help|-h)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  --dry-run      Check status and log alert/recovery without sending Telegram message"
      echo "  --status       Print live channel status JSON and exit"
      echo "  --test         Send a test message to Telegram to verify credentials"
      echo "  --reset-state  Reset the anti-spam state file ($STATE_FILE)"
      echo "  --help, -h     Show this help"
      exit 0
      ;;
  esac
done

if [[ "$RESET_STATE" -eq 1 ]]; then
  rm -f "$STATE_FILE"
  echo "[zalo-health] Anti-spam state file removed: $STATE_FILE"
  exit 0
fi

# Export configuration for Python probe subshell
export STATE_FILE
export ENV_FILE
export CHANNELS
export DRY_RUN
export TEST_MODE
export PRINT_STATUS

# Execute Python probe & alerter
python3 - <<'EOF'
import socket
import base64
import os
import struct
import json
import sys
import datetime
import urllib.request
import urllib.parse
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
import subprocess

state_file = os.environ.get("STATE_FILE", "/tmp/goclaw-zalo-health.state")
env_file = os.environ.get("ENV_FILE", "/opt/deploy/goclaw/.env")
channels_to_check = [c.strip() for c in os.environ.get("CHANNELS", "bao-su-de,zalo-bot").split(",") if c.strip()]
dry_run = os.environ.get("DRY_RUN", "0") in ("1", "true", "True")
test_mode = os.environ.get("TEST_MODE", "0") in ("1", "true", "True")
print_status = os.environ.get("PRINT_STATUS", "0") in ("1", "true", "True")

now_iso = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")

# 1. Read environment variables
env_vars = {}
if os.path.exists(env_file):
    with open(env_file) as f:
        for line in f:
            if '=' in line and not line.startswith('#'):
                k, v = line.strip().split('=', 1)
                env_vars[k] = v

gw_token = env_vars.get("GOCLAW_GATEWAY_TOKEN", "")
enc_key = env_vars.get("GOCLAW_ENCRYPTION_KEY", "")

# Priority Telegram credentials from env / .env file before DB fallback
env_tg_token = os.environ.get("TELEGRAM_ALERT_BOT_TOKEN", "").strip() or env_vars.get("TELEGRAM_ALERT_BOT_TOKEN", "").strip()
env_tg_chat_id = os.environ.get("TELEGRAM_ALERT_CHAT_ID", "").strip() or env_vars.get("TELEGRAM_ALERT_CHAT_ID", "").strip()

# 2. Extract Telegram bot credentials & owner chat_id from PostgreSQL (with env priority)
def get_telegram_info():
    token = env_tg_token or None
    chat_id = env_tg_chat_id or os.environ.get("TELEGRAM_CHAT_ID", "").strip() or None
    
    if not token and enc_key:
        try:
            kbytes = bytes.fromhex(enc_key) if len(enc_key) == 64 else (
                base64.b64decode(enc_key) if len(enc_key) == 44 and enc_key.endswith("=") else enc_key.encode()
            )
            aesgcm = AESGCM(kbytes)
            out = subprocess.check_output(
                ["docker", "exec", "goclaw-postgres-1", "psql", "-U", "goclaw", "-d", "goclaw", "-t", "-A", "-c",
                 "SELECT encode(credentials, 'escape') FROM channel_instances WHERE name = 'telegram';"],
                stderr=subprocess.DEVNULL
            ).decode().strip()
            if out.startswith("aes-gcm:"):
                raw = base64.b64decode(out[len("aes-gcm:"):])
                pt = json.loads(aesgcm.decrypt(raw[:12], raw[12:], None))
                token = pt.get("token")
        except Exception as e:
            sys.stderr.write(f"[zalo-health] Error decrypting Telegram bot token: {e}\n")

    if not chat_id:
        try:
            cid_out = subprocess.check_output(
                ["docker", "exec", "goclaw-postgres-1", "psql", "-U", "goclaw", "-d", "goclaw", "-t", "-A", "-c",
                 "SELECT sender_id FROM channel_contacts WHERE channel_type = 'telegram' LIMIT 1;"],
                stderr=subprocess.DEVNULL
            ).decode().strip()
            if cid_out:
                chat_id = cid_out
        except Exception as e:
            sys.stderr.write(f"[zalo-health] Error querying Telegram chat_id: {e}\n")

    return token, chat_id

# 3. Query GoClaw Gateway via WebSocket RPC (channels.status)
def query_channels_status():
    try:
        s = socket.socket()
        s.settimeout(5)
        s.connect(("127.0.0.1", 18790))
        key = base64.b64encode(os.urandom(16)).decode()
        req = (
            f"GET /ws HTTP/1.1\r\n"
            f"Host: 127.0.0.1:18790\r\n"
            f"Upgrade: websocket\r\n"
            f"Connection: Upgrade\r\n"
            f"Sec-WebSocket-Key: {key}\r\n"
            f"Sec-WebSocket-Version: 13\r\n\r\n"
        )
        s.sendall(req.encode())
        res = s.recv(4096).decode()
        if "101" not in res:
            return None, "WebSocket 101 Switching Protocols failed"

        def send_frame(payload_str):
            data = payload_str.encode()
            length = len(data)
            mask = os.urandom(4)
            masked = bytes(b ^ mask[i % 4] for i, b in enumerate(data))
            if length < 126:
                header = struct.pack("!BB", 0x81, 0x80 | length) + mask
            else:
                header = struct.pack("!BBH", 0x81, 0x80 | 126, length) + mask
            s.sendall(header + masked)

        def recv_frame():
            hdr = s.recv(2)
            if not hdr:
                return None
            length = hdr[1] & 0x7f
            if length == 126:
                length = struct.unpack("!H", s.recv(2))[0]
            elif length == 127:
                length = struct.unpack("!Q", s.recv(8))[0]
            buf = b""
            while len(buf) < length:
                chunk = s.recv(length - len(buf))
                if not chunk:
                    break
                buf += chunk
            return buf.decode(errors="replace")

        # Step 1: connect handshake with gateway token
        send_frame(json.dumps({"type": "req", "id": "1", "method": "connect", "params": {"token": gw_token}}))
        recv_frame()

        # Step 2: query channels.status
        send_frame(json.dumps({"type": "req", "id": "2", "method": "channels.status"}))
        status_frame = recv_frame()
        s.close()

        data = json.loads(status_frame)
        return data.get("payload", {}).get("channels", {}), None
    except Exception as e:
        return None, str(e)

# 4. Send Telegram Message
def send_telegram(token, chat_id, text):
    if dry_run:
        print(f"[zalo-health] [DRY RUN] Would send to Telegram chat {chat_id}:\n{text}")
        return True
    if not token or not chat_id:
        sys.stderr.write(f"[zalo-health] Cannot send Telegram: missing token ({bool(token)}) or chat_id ({chat_id})\n")
        return False
    try:
        url = f"https://api.telegram.org/bot{token}/sendMessage"
        payload = json.dumps({
            "chat_id": chat_id,
            "text": text,
            "parse_mode": "HTML"
        }).encode("utf-8")
        req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status == 200
    except Exception as e:
        sys.stderr.write(f"[zalo-health] Telegram API request failed: {e}\n")
        return False

# 5. Handle Test Mode
tg_token, tg_chat_id = get_telegram_info()
if test_mode:
    print(f"[zalo-health] Sending test notification to Telegram chat {tg_chat_id}...")
    msg = (
        f"🧪 <b>[TEST] GoClaw Zalo Health Monitor</b>\n"
        f"• Thời gian: <code>{now_iso}</code>\n"
        f"• Kênh giám sát: <code>{', '.join(channels_to_check)}</code>\n"
        f"• Hệ thống thông báo Telegram hoạt động bình thường!"
    )
    ok = send_telegram(tg_token, tg_chat_id, msg)
    if ok:
        print("[zalo-health] Test message sent successfully!")
        sys.exit(0)
    else:
        print("[zalo-health] Failed to send test message.")
        sys.exit(1)

# 6. Fetch live channels status
live_channels, gw_err = query_channels_status()

if print_status:
    if gw_err:
        print(json.dumps({"error": gw_err}, indent=2))
        sys.exit(1)
    else:
        filtered = {k: v for k, v in live_channels.items() if k in channels_to_check}
        print(json.dumps(filtered, indent=2))
        sys.exit(0)

# 7. Read state file for anti-spam
state_data = {}
if os.path.exists(state_file):
    try:
        with open(state_file, "r") as f:
            state_data = json.load(f)
    except Exception as e:
        state_data = {}

state_changed = False

for ch_name in channels_to_check:
    prev_state = state_data.get(ch_name, "OK")
    
    if gw_err:
        curr_state = "ERROR"
        curr_summary = f"Gateway Unreachable: {gw_err}"
        curr_detail = "Cannot connect to GoClaw 127.0.0.1:18790"
    else:
        info = live_channels.get(ch_name)
        if not info:
            curr_state = "ERROR"
            curr_summary = "Channel not found in gateway status"
            curr_detail = "Channel may not be registered"
        else:
            is_running = info.get("running", False)
            ch_health = info.get("state", "unknown")
            curr_summary = info.get("summary", "")
            curr_detail = info.get("detail", "")
            
            if is_running and ch_health == "healthy":
                curr_state = "OK"
            else:
                curr_state = "ERROR"
                if not is_running:
                    curr_summary = f"Stopped (state={ch_health})"

    # Check for State Transitions
    if prev_state != "ERROR" and curr_state == "ERROR":
        # Transition: OK -> ERROR (Alert!)
        print(f"[zalo-health] ALERT: Channel {ch_name} transition {prev_state} -> ERROR ({curr_summary})")
        alert_msg = (
            f"🚨 <b>[CẢNH BÁO] Kênh Zalo bị gián đoạn!</b>\n\n"
            f"• <b>Kênh:</b> <code>{ch_name}</code>\n"
            f"• <b>Trạng thái:</b> <code>{curr_state}</code>\n"
            f"• <b>Thông tin:</b> {curr_summary}\n"
        )
        if curr_detail:
            alert_msg += f"• <b>Chi tiết:</b> <i>{curr_detail}</i>\n"
        alert_msg += (
            f"• <b>Thời gian:</b> <code>{now_iso}</code>\n\n"
            f"👉 <i>Vui lòng kiểm tra lại phiên đăng nhập Zalo hoặc Gateway GoClaw!</i>"
        )
        send_telegram(tg_token, tg_chat_id, alert_msg)
        state_data[ch_name] = "ERROR"
        state_changed = True

    elif prev_state == "ERROR" and curr_state == "OK":
        # Transition: ERROR -> OK (Recovery!)
        print(f"[zalo-health] RECOVERED: Channel {ch_name} transition ERROR -> OK")
        recovery_msg = (
            f"✅ <b>[PHỤC HỒI] Kênh Zalo đã kết nối lại!</b>\n\n"
            f"• <b>Kênh:</b> <code>{ch_name}</code>\n"
            f"• <b>Trạng thái:</b> <code>healthy (Connected)</code>\n"
            f"• <b>Thời gian:</b> <code>{now_iso}</code>\n\n"
            f"🎉 <i>Hệ thống nhắn tin Zalo đã hoạt động bình thường trở lại.</i>"
        )
        send_telegram(tg_token, tg_chat_id, recovery_msg)
        state_data[ch_name] = "OK"
        state_changed = True

    else:
        # No state transition: OK -> OK or ERROR -> ERROR (Anti-spam suppression)
        print(f"[zalo-health] Status {ch_name}: {curr_state} (unchanged, anti-spam quiet)")
        if ch_name not in state_data:
            state_data[ch_name] = curr_state
            state_changed = True

# 8. Save updated state
if state_changed or not os.path.exists(state_file):
    try:
        with open(state_file, "w") as f:
            json.dump(state_data, f, indent=2)
    except Exception as e:
        sys.stderr.write(f"[zalo-health] Failed to write state file {state_file}: {e}\n")

EOF
