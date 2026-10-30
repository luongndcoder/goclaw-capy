---
name: gws-tenant-routing
description: Google Workspace via gws SecureCLI (Master/TaiDao). Prefer helpers +read/+append/+write. Use for Sheets Docs Drive Calendar instead of MCP.
---

# gws + SecureCLI (GoClaw Direct Exec)

## Critical: Direct Exec Mode (no shell)

`exec` runs **argv only** — no shell, no quote stripping by bash, but the **LLM must still emit valid JSON strings**.

### WRONG (invalid JSON — will fail validation exit 3)
```
gws sheets spreadsheets get --params {spreadsheetId:ABC,fields:properties}
```

### RIGHT — use helpers (preferred, fewer JSON pitfalls)
```
gws sheets +read --spreadsheet 1wBumKqEVXLi54EwA-0kD7vECq3bW5IBYz9CwWmh9aQY --range Sheet1!A1:C10
gws sheets +append --spreadsheet SPREADSHEET_ID --values "Name,Score"
gws docs +write --document DOCUMENT_ID --text "hello"
gws drive +upload ./file.pdf --name "Report"
gws calendar +agenda
gws drive files list --params {"pageSize":5}
```

### RIGHT — Discovery method with real JSON object (keys AND string values quoted)
Pass `--params` as ONE argument whose value is strict JSON:

```
gws sheets spreadsheets get --params {"spreadsheetId":"1wBumKqEVXLi54EwA-0kD7vECq3bW5IBYz9CwWmh9aQY","fields":"properties,sheets.properties"}
```

Rules for `--params` / `--json`:
1. Keys must be double-quoted strings: `"spreadsheetId"`
2. String values must be double-quoted: `"1wBu..."`
3. Never use bare `{key:value}` JS object style
4. Do not wrap the whole command in shell quotes beyond what the tool needs — credentialed exec takes separate argv; put the JSON object as the value of `--params`

## Auth
SecureCLI injects credentials. Never run `gws auth login|export|logout|setup`. Never read `/creds` or credential files.

## Failures
- exit 2 / invalid_grant → admin reauth runbook
- exit 3 validation / Invalid --params JSON → fix JSON quoting or switch to `+read`
- exit 4 discovery / Permission denied → admin path/permissions (should be fixed to /app/data/gws/...)

## Tenant
Master and TaiDao each have their own SecureCLI env — do not set CONFIG_DIR manually.
