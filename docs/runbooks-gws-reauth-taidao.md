# TaiDao — Google re-auth cho gws (1 lần, ~5 phút)

Bạn cần đăng nhập Google account của **TaiDao** (không dùng account Master).

## Trên máy có browser (Mac/Windows)

1. Cài gws:
```bash
npm i -g @googleworkspace/cli@0.22.5
```

2. Lấy OAuth client (Desktop) từ admin — file `gws-client-taidao.json`  
   (admin đã có trên VPS: `/root/credentials/gws-client-taidao.json`)

3. Login:
```bash
export GOOGLE_WORKSPACE_CLI_CONFIG_DIR=/tmp/gws-auth-taidao
export GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file
mkdir -p "$GOOGLE_WORKSPACE_CLI_CONFIG_DIR"
cp /path/to/gws-client-taidao.json "$GOOGLE_WORKSPACE_CLI_CONFIG_DIR/client_secret.json"

gws auth login -s drive,sheets,docs,calendar
# Browser mở → chọn đúng account TaiDao → Allow
```

4. Export:
```bash
gws auth export --unmasked > /tmp/gws-taidao.json
```

5. Gửi file cho admin (SCP/AirDrop/secure channel — **không** gửi chat public):
```bash
# admin sẽ chạy:
# scp gws-taidao.json root@VPS:/tmp/
# /opt/deploy/goclaw/scripts/gws-reauth-install.sh taidao /tmp/gws-taidao.json
# /opt/deploy/goclaw/scripts/gws-health.sh
```

## Kiểm tra thành công
- Email sau login phải là account TaiDao (khác `luongnd.coder@gmail.com`)
- Health: `[gws-health] OK tenant=taidao`

## Lưu ý
- Nếu app OAuth còn **Testing**, token có thể hết ~7 ngày → nên **Publish Production**.
- Không share `refresh_token` / `gws-taidao.json` ra nhóm chat.
