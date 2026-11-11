"""mcp_sheets_wrapper.py — GoClaw VPS wrapper for mcp-google-sheets (v2).

- Fixes the 0.6.3 double-range bug (idempotent patch of installed server.py).
- Adds 4 EXTRA formatting/structure tools using the SAME service-account auth:
    * format_sheet_range      — background/text color, bold, italic, font size,
                                alignment, wrap (Sheets API updateCells)
    * set_range_borders       — border style for a range (updateBorders)
    * resize_sheet_dimensions — column width / row height (updateDimensionProperties)
    * rename_sheet_tab        — rename a sheet tab (updateSpreadsheetProperties)
  These give the agent REAL formatting capability so it never has to fabricate
  "I changed the colors" — it can genuinely do it (covered by tool_hints).

Run (SSE transport, same as before):
    python3 /wrapper.py
"""

import os
import re
from typing import Any, Dict

# ── Patch installed server.py (idempotent) — double-range bug ──────
SERVER_PY = "/usr/local/lib/python3.11/site-packages/mcp_google_sheets/server.py"
if os.path.exists(SERVER_PY):
    src = open(SERVER_PY, encoding="utf-8").read()
    old = 'full_range = f"{sheet}!{range}"'
    new = 'full_range = range if "!" in range else f"{sheet}!{range}"'
    if old in src:
        src = src.replace(old, new)
        open(SERVER_PY, "w", encoding="utf-8").write(src)
        print("[wrapper] patched double-range bug", flush=True)

# ── Import the original server & register extra tools on it ─────────
from mcp_google_sheets.server import mcp  # noqa: E402
from mcp.server.fastmcp import Context  # noqa: E402


# ── Helper: get service from lifespan context ───────────────────────
def _svc(ctx: Context):
    return ctx.request_context.lifespan_context.sheets_service


# Color helpers: accept "#RRGGBB" hex string -> Sheets Color object.
_HEX_RE = re.compile(r"^#?([0-9a-fA-F]{6})$")


def _color(val):
    """Convert '#RRGGBB' to {'red': r, 'green': g, 'blue': b} floats (0-1)."""
    if val is None:
        return None
    m = _HEX_RE.match(str(val).strip())
    if not m:
        raise ValueError(f"Invalid color '{val}'; expected #RRGGBB")
    h = m.group(1)
    return {
        "red": int(h[0:2], 16) / 255.0,
        "green": int(h[2:4], 16) / 255.0,
        "blue": int(h[4:6], 16) / 255.0,
    }


@mcp.tool()
def format_sheet_range(
    spreadsheet_id: str,
    range: str,
    background_color: str = None,
    text_color: str = None,
    bold: bool = None,
    italic: bool = None,
    font_size: float = None,
    horizontal_alignment: str = None,
    vertical_alignment: str = None,
    wrap_strategy: str = None,
    ctx: Context = None,
) -> str:
    """Format a cell range in a Google Sheet: background/text color (#RRGGBB hex),
    bold, italic, font size, alignment (LEFT/CENTER/RIGHT, TOP/MIDDLE/BOTTOM),
    and text wrap (WRAP/CLIP/OVERFLOW_CELL). range is A1 notation (optionally
    'Sheet1!A1:C10'); defaults to first sheet if no sheet prefix. Returns a
    confirmation with the applied range."""
    if not spreadsheet_id or not range:
        return "Error: spreadsheet_id and range are required"
    svc = _svc(ctx)
    # Resolve sheet id: accept "Sheet1!A1:B2" or plain "A1:B2" (first sheet).
    sheet_name = None
    a1 = range
    if "!" in range:
        sheet_name, _, a1 = range.partition("!")
    meta = svc.spreadsheets().get(spreadsheetId=spreadsheet_id).execute()
    sheet_id = None
    for sh in meta["sheets"]:
        p = sh["properties"]
        if sheet_name is None or p["title"] == sheet_name:
            sheet_id = p["sheetId"]
            break
    if sheet_id is None:
        return f"Error: sheet '{sheet_name}' not found in spreadsheet"

    fmt = {}
    if background_color is not None:
        fmt["backgroundColor"] = _color(background_color)
    if text_color is not None:
        # Foreground/text color lives INSIDE textFormat, not at top level.
        fmt.setdefault("textFormat", {})["foregroundColor"] = _color(text_color)
    if bold is not None:
        fmt.setdefault("textFormat", {})["bold"] = bold
    if italic is not None:
        fmt.setdefault("textFormat", {})["italic"] = italic
    if font_size is not None:
        fmt.setdefault("textFormat", {})["fontSize"] = float(font_size)
    if horizontal_alignment:
        ha = str(horizontal_alignment).upper()
        if ha not in ("LEFT", "CENTER", "RIGHT"):
            return f"Error: horizontal_alignment must be LEFT/CENTER/RIGHT, got {horizontal_alignment}"
        fmt["horizontalAlignment"] = ha
    if vertical_alignment:
        va = str(vertical_alignment).upper()
        if va not in ("TOP", "MIDDLE", "BOTTOM"):
            return f"Error: vertical_alignment must be TOP/MIDDLE/BOTTOM, got {vertical_alignment}"
        fmt["verticalAlignment"] = va
    if wrap_strategy:
        ws = str(wrap_strategy).upper()
        if ws not in ("WRAP", "CLIP", "OVERFLOW_CELL"):
            return f"Error: wrap_strategy must be WRAP/CLIP/OVERFLOW_CELL, got {wrap_strategy}"
        fmt["wrapStrategy"] = ws

    if not fmt:
        return "Error: at least one formatting attribute is required"

    # RepeatCell applies the format to EVERY cell in the range (correct for
    # both a specific A1 range and the whole sheet).
    import re as _re
    grid = None
    if a1:
        m = _re.match(r"^([A-Z]+)(\d+):([A-Z]+)(\d+)$", a1.strip())
        if not m:
            return f"Error: invalid A1 range '{a1}'"
        c1, r1, c2, r2 = m.groups()
        def _col(x):
            n = 0
            for ch in x:
                n = n * 26 + (ord(ch.upper()) - 64)
            return n - 1
        grid = {
            "sheetId": sheet_id,
            "startRowIndex": int(r1) - 1,
            "endRowIndex": int(r2),
            "startColumnIndex": _col(c1),
            "endColumnIndex": _col(c2),
        }
    else:
        grid = {
            "sheetId": sheet_id,
            "startRowIndex": 0,
            "endRowIndex": 1000000,
            "startColumnIndex": 0,
            "endColumnIndex": 1000000,
        }

    body = {
        "requests": [{
            "repeatCell": {
                "range": grid,
                "cell": {"userEnteredFormat": fmt},
                # repeatCell.fields uses paths from the cell root; make the
                # format keys explicit as userEnteredFormat.* (Google rejects
                # bare top-level names like "backgroundColor").
                "fields": ",".join("userEnteredFormat." + k for k in fmt.keys()),
            }
        }]
    }
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    display = f"{sheet_name or 'first sheet'}!{a1}" if a1 else "entire sheet"
    return f"Formatted {display} in {spreadsheet_id}. Applied: {','.join(fmt.keys())}."


@mcp.tool()
def set_range_borders(
    spreadsheet_id: str,
    range: str,
    style: str = "SOLID",
    color: str = "#000000",
    top: bool = True,
    bottom: bool = True,
    left: bool = True,
    right: bool = True,
    ctx: Context = None,
) -> str:
    """Apply borders to a cell range (A1 notation, e.g. 'Sheet1!A1:H10').
    style: SOLID/DOTTED/DASHED/DOUBLE. color: #RRGGBB. Apply per-edge with
    top/bottom/left/right booleans (default all four)."""
    if not spreadsheet_id or not range:
        return "Error: spreadsheet_id and range are required"
    svc = _svc(ctx)
    sheet_name, _, a1 = range.partition("!") if "!" in range else (None, "", range)
    meta = svc.spreadsheets().get(spreadsheetId=spreadsheet_id).execute()
    sheet_id = None
    for sh in meta["sheets"]:
        p = sh["properties"]
        if sheet_name is None or p["title"] == sheet_name:
            sheet_id = p["sheetId"]
            break
    if sheet_id is None:
        return f"Error: sheet '{sheet_name}' not found"
    import re as _re
    m = _re.match(r"^([A-Z]+)(\d+):([A-Z]+)(\d+)$", a1.strip())
    if not m:
        return f"Error: invalid A1 range '{a1}'"
    c1, r1, c2, r2 = m.groups()
    def _col(x):
        n = 0
        for ch in x:
            n = n * 26 + (ord(ch.upper()) - 64)
        return n - 1
    col = {"red": int(color.lstrip("#")[0:2], 16) / 255.0,
           "green": int(color.lstrip("#")[2:4], 16) / 255.0,
           "blue": int(color.lstrip("#")[4:6], 16) / 255.0}
    style = str(style).upper()
    if style not in ("SOLID", "DOTTED", "DASHED", "DOUBLE"):
        return f"Error: style must be SOLID/DOTTED/DASHED/DOUBLE"
    edge = {"style": style, "color": col, "width": 1}
    borders = {}
    if top:
        borders["top"] = edge
    if bottom:
        borders["bottom"] = edge
    if left:
        borders["left"] = edge
    if right:
        borders["right"] = edge
    body = {"requests": [{
        "updateBorders": {
            "range": {
                "sheetId": sheet_id,
                "startRowIndex": int(r1) - 1,
                "endRowIndex": int(r2),
                "startColumnIndex": _col(c1),
                "endColumnIndex": _col(c2),
            },
            **borders,
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Applied {style} borders ({color}) to {sheet_name or 'first sheet'}!{a1}."


@mcp.tool()
def resize_sheet_dimensions(
    spreadsheet_id: str,
    dimension: str,
    start_index: int,
    end_index: int,
    pixel_size: int,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Resize columns (dimension='COLUMNS') or rows ('ROWS') in a Google Sheet.
    start_index/end_index are 0-based, e.g. 0..20 for the first 20 columns.
    pixel_size is the new width/height in pixels. sheet is the tab name
    (optional; defaults to first tab)."""
    if dimension.upper() not in ("COLUMNS", "ROWS"):
        return "Error: dimension must be COLUMNS or ROWS"
    if pixel_size <= 0 or start_index < 0 or end_index <= start_index:
        return "Error: invalid pixel_size / indices"
    svc = _svc(ctx)
    meta = svc.spreadsheets().get(spreadsheetId=spreadsheet_id).execute()
    sheet_id = None
    for sh in meta["sheets"]:
        p = sh["properties"]
        if sheet is None or p["title"] == sheet:
            sheet_id = p["sheetId"]
            break
    if sheet_id is None:
        return f"Error: sheet '{sheet}' not found"
    body = {"requests": [{
        "updateDimensionProperties": {
            "range": {
                "sheetId": sheet_id,
                "dimension": dimension.upper(),
                "startIndex": int(start_index),
                "endIndex": int(end_index),
            },
            "properties": {"pixelSize": int(pixel_size)},
            "fields": "pixelSize",
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return (f"Resized {dimension.upper()} {start_index}..{end_index} "
            f"to {pixel_size}px in {sheet or 'first sheet'}.")


@mcp.tool()
def rename_sheet_tab(
    spreadsheet_id: str,
    sheet: str,
    new_title: str,
    ctx: Context = None,
) -> str:
    """Rename a sheet tab in a spreadsheet. sheet is the CURRENT tab name;
    new_title is the new name. Returns confirmation."""
    if not new_title or not sheet:
        return "Error: sheet and new_title are required"
    svc = _svc(ctx)
    meta = svc.spreadsheets().get(spreadsheetId=spreadsheet_id).execute()
    sheet_id = None
    for sh in meta["sheets"]:
        if sh["properties"]["title"] == sheet:
            sheet_id = sh["properties"]["sheetId"]
            break
    if sheet_id is None:
        return f"Error: sheet '{sheet}' not found"
    body = {"requests": [{
        "updateSheetProperties": {
            "properties": {"sheetId": sheet_id, "title": new_title},
            "fields": "title",
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Renamed tab '{sheet}' to '{new_title}' in {spreadsheet_id}."


# ── Helper: resolve sheetId from tab name (or first tab) ────────────
def _sheet_id(svc, spreadsheet_id, sheet=None):
    meta = svc.spreadsheets().get(spreadsheetId=spreadsheet_id).execute()
    for sh in meta["sheets"]:
        p = sh["properties"]
        if sheet is None or p["title"] == sheet:
            return p["sheetId"]
    return None


# ── Helper: sanitize model-supplied batchUpdate requests ────────────
# Google returns 400 "Invalid JSON payload ... Unknown name \"reason\"" when
# the model attaches explanatory fields to request objects (a common LLM
# habit). Strip anything that is not a real batchUpdate operation so a
# well-intentioned call still succeeds instead of failing loudly.
_BATCH_OPS = {
    "addSheet", "deleteSheet", "duplicateSheet", "updateSheetProperties",
    "updateCells", "updateBorders", "updateDimensionProperties",
    "insertDimension", "deleteDimension", "insertRange", "deleteRange",
    "moveDimension", "autoResizeDimensions", "mergeCells", "unmergeCells",
    "repeatCell", "copyPaste", "cutPaste", "pasteData", "findReplace",
    "addFilterView", "updateFilterView", "deleteFilterView",
    "addConditionalFormatRule", "updateConditionalFormatRule",
    "deleteConditionalFormatRule", "moveConditionalFormatRule",
    "addProtectedRange", "updateProtectedRange", "deleteProtectedRange",
    "addNamedRange", "updateNamedRange", "deleteNamedRange",
    "addBanding", "updateBanding", "deleteBanding",
    "addChart", "updateChart", "deleteChart", "updateEmbeddedObjectPosition",
    "addDataSource", "updateDataSource", "deleteDataSource",
    "addDeveloperMetadata", "updateDeveloperMetadata",
    "deleteDeveloperMetadata", "sortRange", "trimWhitespace",
    "setBasicFilter", "clearBasicFilter", "randomizeRange",
    "setDataValidation",
}
# Advisory keys LLMs like to add; dropped rather than rejected.
_NOISE_KEYS = {"reason", "explanation", "note", "comment", "why", "description"}


def _sanitize_requests(requests):
    """Drop noise keys and non-operation entries from batchUpdate requests.
    Returns (clean_requests, dropped_note)."""
    if not isinstance(requests, list):
        return requests, "requests must be a list"
    clean = []
    dropped = []
    for item in requests:
        if not isinstance(item, dict):
            continue
        ops = {k: v for k, v in item.items() if k in _BATCH_OPS}
        if not ops:
            dropped.extend(k for k in item.keys())
            continue
        for k in item:
            if k not in _BATCH_OPS and k in _NOISE_KEYS:
                dropped.append(k)
        clean.append(ops)
    note = ""
    if dropped:
        note = (" (bo cac field khong hop le: " +
                ", ".join(sorted(set(dropped))) + ")")
    return clean, note


# ── 10 extra productivity tools (Sheets batchUpdate power) ─────────

@mcp.tool()
def freeze_panes(
    spreadsheet_id: str,
    freeze_rows: int = 1,
    freeze_columns: int = 0,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Freeze (pin) N top rows and M left columns so they stay visible when
    scrolling. freeze_rows/freeze_columns are counts (e.g. 1 = header row).
    Returns confirmation."""
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    body = {"requests": [{
        "updateSheetProperties": {
            "properties": {
                "sheetId": sid,
                "gridProperties": {
                    "frozenRowCount": int(freeze_rows),
                    "frozenColumnCount": int(freeze_columns),
                },
            },
            "fields": "gridProperties.frozenRowCount,gridProperties.frozenColumnCount",
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return (f"Froze {freeze_rows} row(s) and {freeze_columns} column(s) "
            f"in {sheet or 'first sheet'}.")


@mcp.tool()
def set_number_format(
    spreadsheet_id: str,
    range: str,
    number_type: str = "NUMBER",
    pattern: str = None,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Set number format for a range. number_type: NUMBER, CURRENCY, DATE,
    DATE_TIME, PERCENT, TEXT, or AUTO. pattern: optional format pattern
    (e.g. '#,##0₫' for VND, 'dd/MM/yyyy' for dates). range is A1 notation.
    Returns confirmation."""
    if not spreadsheet_id or not range:
        return "Error: spreadsheet_id and range are required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    sheet_prefix = f"{sheet}!" if sheet else ""
    a1 = range[len(sheet_prefix):] if range.startswith(sheet_prefix) else range
    # Convert A1 to grid indices (support A1:B2 form)
    import re as _re
    m = _re.match(r"^([A-Z]+)(\d+)(?::([A-Z]+)(\d+))?$", a1.strip())
    if not m:
        return f"Error: invalid A1 range '{a1}'"
    c1, r1 = m.group(1), m.group(2)
    c2, r2 = m.group(3) or c1, m.group(4) or r1
    def _col(x):
        n = 0
        for ch in x:
            n = n * 26 + (ord(ch.upper()) - 64)
        return n - 1
    num_fmt = {}
    nt = str(number_type).upper()
    valid = ("NUMBER", "CURRENCY", "DATE", "DATE_TIME", "PERCENT", "TEXT", "AUTO")
    if nt not in valid:
        return f"Error: number_type must be one of {valid}"
    if pattern is not None:
        num_fmt["pattern"] = str(pattern)
    if nt != "AUTO":
        num_fmt["type"] = nt
    body = {"requests": [{
        "repeatCell": {
            "range": {
                "sheetId": sid,
                "startRowIndex": int(r1) - 1,
                "endRowIndex": int(r2),
                "startColumnIndex": _col(c1),
                "endColumnIndex": _col(c2),
            },
            "cell": {"userEnteredFormat": {"numberFormat": num_fmt}},
            "fields": "userEnteredFormat.numberFormat",
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Set {nt} format{(' (' + pattern + ')') if pattern else ''} on {a1} in {sheet or 'first sheet'}."


@mcp.tool()
def conditional_format(
    spreadsheet_id: str,
    range: str,
    rule_type: str = "TEXT",
    operand: str = None,
    background_color: str = "#FFF2CC",
    text_color: str = None,
    bold: bool = None,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Add a conditional formatting rule to a range. rule_type:
      TEXT  — highlight cells equal to operand
      BLANK / NOT_BLANK — highlight empty / non-empty cells
      CONTAINS — highlight cells containing operand
      NUMBER — highlight numeric cells (operand like '>1000000' or '=500000')
    background_color is the highlight (#RRGGBB). Returns confirmation."""
    if not spreadsheet_id or not range:
        return "Error: spreadsheet_id and range are required"
    if rule_type in ("TEXT", "CONTAINS") and operand is None:
        return "Error: operand required for TEXT/CONTAINS"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    sheet_prefix = f"{sheet}!" if sheet else ""
    a1 = range[len(sheet_prefix):] if range.startswith(sheet_prefix) else range
    import re as _re
    m = _re.match(r"^([A-Z]+)(\d+)(?::([A-Z]+)(\d+))?$", a1.strip())
    if not m:
        return f"Error: invalid A1 range '{a1}'"
    c1, r1, c2, r2 = m.group(1), m.group(2), m.group(3) or m.group(1), m.group(4) or m.group(2)
    def _col(x):
        n = 0
        for ch in x:
            n = n * 26 + (ord(ch.upper()) - 64)
        return n - 1
    grid = {"sheetId": sid, "startRowIndex": int(r1) - 1, "endRowIndex": int(r2),
            "startColumnIndex": _col(c1), "endColumnIndex": _col(c2)}
    fmt = {"backgroundColor": _color(background_color)}
    if text_color:
        fmt.setdefault("textFormat", {})["foregroundColor"] = _color(text_color)
    if bold is not None:
        fmt.setdefault("textFormat", {})["bold"] = bool(bold)
    rt = str(rule_type).upper()
    if rt == "TEXT":
        cond = {"type": "TEXT_EQ", "values": [{"userEnteredValue": str(operand)}]}
    elif rt == "CONTAINS":
        cond = {"type": "TEXT_CONTAINS", "values": [{"userEnteredValue": str(operand)}]}
    elif rt == "BLANK":
        cond = {"type": "BLANK"}
    elif rt == "NOT_BLANK":
        cond = {"type": "NOT_BLANK"}
    elif rt == "NUMBER":
        op = str(operand or ">0").strip()
        op_map = {">": "NUMBER_GREATER", ">=": "NUMBER_GREATER_THAN_EQ",
                  "<": "NUMBER_LESS", "<=": "NUMBER_LESS_THAN_EQ",
                  "=": "NUMBER_EQ", "!=": "NUMBER_NOT_EQ"}
        op_name = next((v for k, v in op_map.items() if op.startswith(k)), None)
        if op_name is None:
            return "Error: NUMBER operand must start with >, >=, <, <=, =, !="
        value = op.lstrip(">=<!")
        cond = {"type": op_name, "values": [{"userEnteredValue": value.strip()}]}
    else:
        return "Error: rule_type must be TEXT/CONTAINS/BLANK/NOT_BLANK/NUMBER"
    body = {"requests": [{
        "addConditionalFormatRule": {
            "rule": {"ranges": [grid], "booleanRule": {"condition": cond, "format": fmt}},
            "index": 0,
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Added {rt} conditional format on {a1}."


@mcp.tool()
def data_validation_dropdown(
    spreadsheet_id: str,
    range: str,
    options: str,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Add a dropdown data-validation to a range. options: comma-separated
    values (e.g. 'Chưa thanh toán,Đã thanh toán,Đã xóa'). range is A1
    notation. Returns confirmation."""
    if not spreadsheet_id or not range or not options:
        return "Error: spreadsheet_id, range and options are required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    sheet_prefix = f"{sheet}!" if sheet else ""
    a1 = range[len(sheet_prefix):] if range.startswith(sheet_prefix) else range
    import re as _re
    m = _re.match(r"^([A-Z]+)(\d+)(?::([A-Z]+)(\d+))?$", a1.strip())
    if not m:
        return f"Error: invalid A1 range '{a1}'"
    c1, r1, c2, r2 = m.group(1), m.group(2), m.group(3) or m.group(1), m.group(4) or m.group(2)
    def _col(x):
        n = 0
        for ch in x:
            n = n * 26 + (ord(ch.upper()) - 64)
        return n - 1
    vals = [{"userEnteredValue": o.strip()} for o in options.split(",") if o.strip()]
    if not vals:
        return "Error: options must contain at least one value"
    grid = {"sheetId": sid, "startRowIndex": int(r1) - 1, "endRowIndex": int(r2),
            "startColumnIndex": _col(c1), "endColumnIndex": _col(c2)}
    body = {"requests": [{
        "setDataValidation": {
            "range": grid,
            "rule": {
                "condition": {"type": "ONE_OF_LIST", "values": vals},
                "strict": True,
                "showCustomUi": True,
            },
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Added dropdown [{', '.join(v['userEnteredValue'] for v in vals)}] on {a1}."


@mcp.tool()
def append_rows(
    spreadsheet_id: str,
    sheet: str,
    rows: str,
    ctx: Context = None,
) -> str:
    """Append one or more data rows to the FIRST EMPTY row of a sheet.
    rows: JSON array of arrays, e.g. '[[\"Anh Tuấn\",\"Nợ tiền ăn\",\"300000\"],[\"Chị Hoa\",\"Nợ tiền nước\",\"150000\"]]'.
    Returns the appended range."""
    if not spreadsheet_id or not rows:
        return "Error: spreadsheet_id and rows are required"
    import json as _json
    try:
        data = _json.loads(rows)
    except Exception:
        return "Error: rows must be a JSON array of arrays"
    if not isinstance(data, list) or not all(isinstance(r, list) for r in data):
        return "Error: rows must be a JSON array of arrays"
    svc = _svc(ctx)
    r = svc.spreadsheets().values().append(
        spreadsheetId=spreadsheet_id, range=f"{sheet}!A1",
        valueInputOption="USER_ENTERED", insertDataOption="INSERT_ROWS",
        body={"values": data}).execute()
    updated = r.get("updates", {}).get("updatedRange", "")
    return f"Appended {len(data)} row(s) at {updated}."


@mcp.tool()
def merge_cells(
    spreadsheet_id: str,
    range: str,
    mode: str = "ALL",
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Merge cells in a range. mode: ALL (row+column), ROWS (merge each row),
    COLUMNS (merge each column). Returns confirmation."""
    if not spreadsheet_id or not range:
        return "Error: spreadsheet_id and range are required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    sheet_prefix = f"{sheet}!" if sheet else ""
    a1 = range[len(sheet_prefix):] if range.startswith(sheet_prefix) else range
    import re as _re
    m = _re.match(r"^([A-Z]+)(\d+)(?::([A-Z]+)(\d+))?$", a1.strip())
    if not m:
        return f"Error: invalid A1 range '{a1}'"
    c1, r1, c2, r2 = m.group(1), m.group(2), m.group(3) or m.group(1), m.group(4) or m.group(2)
    def _col(x):
        n = 0
        for ch in x:
            n = n * 26 + (ord(ch.upper()) - 64)
        return n - 1
    mt = str(mode).upper()
    merge_map = {"ALL": "MERGE_ALL", "ROWS": "MERGE_ROWS", "COLUMNS": "MERGE_COLUMNS"}
    if mt not in merge_map:
        return "Error: mode must be ALL/ROWS/COLUMNS"
    body = {"requests": [{
        "mergeCells": {
            "range": {"sheetId": sid, "startRowIndex": int(r1) - 1, "endRowIndex": int(r2),
                      "startColumnIndex": _col(c1), "endColumnIndex": _col(c2)},
            "mergeType": merge_map[mt],
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Merged {a1} ({mt})."


@mcp.tool()
def auto_resize_columns(
    spreadsheet_id: str,
    sheet: str = None,
    start_index: int = 0,
    end_index: int = 26,
    ctx: Context = None,
) -> str:
    """Auto-fit column widths to their content (AutoResizeDimensions).
    end_index defaults to 26 (columns A..Z). Returns confirmation."""
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    body = {"requests": [{
        "autoResizeDimensions": {
            "dimensions": {
                "sheetId": sid,
                "dimension": "COLUMNS",
                "startIndex": int(start_index),
                "endIndex": int(end_index),
            }
        }
    }]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Auto-resized columns {start_index}..{end_index} in {sheet or 'first sheet'}."


@mcp.tool()
def find_replace(
    spreadsheet_id: str,
    find: str,
    replacement: str,
    sheet: str = None,
    match_case: bool = False,
    ctx: Context = None,
) -> str:
    """Find and replace text across a sheet (or entire spreadsheet if sheet
    omitted). Returns number of replacements."""
    if not spreadsheet_id or find is None or replacement is None:
        return "Error: spreadsheet_id, find and replacement are required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    body = {"requests": [{"findReplace": {
        "find": str(find),
        "replacement": str(replacement),
        "includeFormulas": False,
        "matchCase": bool(match_case),
    }}]}
    if sheet is not None:
        body["requests"][0]["findReplace"]["sheetId"] = sid
    else:
        body["requests"][0]["findReplace"]["allSheets"] = True
    r = svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    n = r.get("replies", [{}])[0].get("findReplace", {}).get("occurrencesChanged", 0)
    return f"Replaced {n} occurrence(s) of '{find}'."


@mcp.tool()
def clear_range(
    spreadsheet_id: str,
    range: str,
    ctx: Context = None,
) -> str:
    """Clear cell VALUES in a range (A1 notation, e.g. 'Sheet1!A5:F10').
    Formatting is preserved. Returns confirmation."""
    if not spreadsheet_id or not range:
        return "Error: spreadsheet_id and range are required"
    svc = _svc(ctx)
    svc.spreadsheets().values().clear(spreadsheetId=spreadsheet_id, range=range).execute()
    return f"Cleared values in {range}."


@mcp.tool()
def insert_remove_rows(
    spreadsheet_id: str,
    action: str,
    start_index: int,
    num: int = 1,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Insert or delete rows in a sheet. action: INSERT or DELETE.
    start_index is 0-based row number; num is how many. For INSERT you may
    optionally give a row index to insert ABOVE. Returns confirmation."""
    if action.upper() not in ("INSERT", "DELETE"):
        return "Error: action must be INSERT or DELETE"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    if action.upper() == "INSERT":
        req = {"insertDimension": {"range": {
            "sheetId": sid, "dimension": "ROWS",
            "startIndex": int(start_index), "endIndex": int(start_index) + int(num)}}}
    else:
        req = {"deleteDimension": {"range": {
            "sheetId": sid, "dimension": "ROWS",
            "startIndex": int(start_index), "endIndex": int(start_index) + int(num)}}}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id,
                                   body={"requests": [req]}).execute()
    return (f"{action.upper()} {num} row(s) at index {start_index} "
            f"in {sheet or 'first sheet'}.")


# ── 7 more power tools ─────────────────────────────────────────────

@mcp.tool()
def add_filter(
    spreadsheet_id: str,
    range: str,
    sheet: str = None,
    clear_existing: bool = True,
    ctx: Context = None,
) -> str:
    """Add a filter view to a range (enables the funnel dropdowns on each
    header cell). range is A1 notation (e.g. 'A1:H10'). Returns confirmation."""
    if not spreadsheet_id or not range:
        return "Error: spreadsheet_id and range are required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    sheet_prefix = f"{sheet}!" if sheet else ""
    a1 = range[len(sheet_prefix):] if range.startswith(sheet_prefix) else range
    import re as _re
    m = _re.match(r"^([A-Z]+)(\d+)(?::([A-Z]+)(\d+))?$", a1.strip())
    if not m:
        return f"Error: invalid A1 range '{a1}'"
    c1, r1, c2, r2 = m.group(1), m.group(2), m.group(3) or m.group(1), m.group(4) or m.group(2)
    def _col(x):
        n = 0
        for ch in x:
            n = n * 26 + (ord(ch.upper()) - 64)
        return n - 1
    requests = []
    if clear_existing:
        requests.append({"clearBasicFilter": {"sheetId": sid}})
    requests.append({"setBasicFilter": {
        "filter": {"range": {
            "sheetId": sid, "startRowIndex": int(r1) - 1, "endRowIndex": int(r2),
            "startColumnIndex": _col(c1), "endColumnIndex": _col(c2)}}}})
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id,
                                   body={"requests": requests}).execute()
    return f"Filter added on {a1}."


@mcp.tool()
def protect_sheet(
    spreadsheet_id: str,
    sheet: str = None,
    description: str = "Protected by agent",
    warning_only: bool = False,
    ctx: Context = None,
) -> str:
    """Protect a sheet tab (block edits, or warn-only). warning_only=True shows
    a warning instead of blocking. Returns the protection id."""
    if not spreadsheet_id:
        return "Error: spreadsheet_id is required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    body = {"requests": [{"addProtectedRange": {
        "protectedRange": {
            "range": {"sheetId": sid},
            "description": str(description),
            "warningOnly": bool(warning_only),
        }}}]}
    r = svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    pid = r.get("replies", [{}])[0].get("addProtectedRange", {}).get("protectedRangeId")
    return f"Protected {sheet or 'first sheet'} ({'warn' if warning_only else 'block edits'}), id={pid}."


@mcp.tool()
def unprotect_sheet(
    spreadsheet_id: str,
    protection_id: int,
    ctx: Context = None,
) -> str:
    """Remove a protection by its id (from protect_sheet). Returns confirmation."""
    if not spreadsheet_id or protection_id is None:
        return "Error: spreadsheet_id and protection_id are required"
    svc = _svc(ctx)
    body = {"requests": [{"deleteProtectedRange": {"protectedRangeId": int(protection_id)}}]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Removed protection {protection_id}."


@mcp.tool()
def set_hyperlink(
    spreadsheet_id: str,
    cell: str,
    url: str,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Set a clickable hyperlink in a cell. cell: single cell A1 (e.g. 'D2').
    url: the link. Optionally the display text is the URL. Returns confirmation."""
    if not spreadsheet_id or not cell or not url:
        return "Error: spreadsheet_id, cell and url are required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    sheet_prefix = f"{sheet}!" if sheet else ""
    a1 = cell[len(sheet_prefix):] if cell.startswith(sheet_prefix) else cell
    import re as _re
    m = _re.match(r"^([A-Z]+)(\d+)$", a1.strip())
    if not m:
        return f"Error: cell must be like 'D2'"
    c, r = m.group(1), int(m.group(2))
    def _col(x):
        n = 0
        for ch in x:
            n = n * 26 + (ord(ch.upper()) - 64)
        return n - 1
    body = {"requests": [{"updateCells": {
        "range": {"sheetId": sid, "startRowIndex": r - 1, "endRowIndex": r,
                  "startColumnIndex": _col(c), "endColumnIndex": _col(c) + 1},
        "rows": [{"values": [{
            "userEnteredValue": {"stringValue": url},
            "userEnteredFormat": {"textFormat": {"link": {"uri": url}}},
        }]}],
        "fields": "userEnteredValue,userEnteredFormat.textFormat.link",
    }}]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Hyperlink set on {a1} -> {url}."


@mcp.tool()
def add_note(
    spreadsheet_id: str,
    cell: str,
    note: str,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Attach a note (yellow sticky) to a cell without changing its value.
    cell: single cell A1. Returns confirmation."""
    if not spreadsheet_id or not cell or not note:
        return "Error: spreadsheet_id, cell and note are required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    sheet_prefix = f"{sheet}!" if sheet else ""
    a1 = cell[len(sheet_prefix):] if cell.startswith(sheet_prefix) else cell
    import re as _re
    m = _re.match(r"^([A-Z]+)(\d+)$", a1.strip())
    if not m:
        return f"Error: cell must be like 'D2'"
    c, r = m.group(1), int(m.group(2))
    def _col(x):
        n = 0
        for ch in x:
            n = n * 26 + (ord(ch.upper()) - 64)
        return n - 1
    body = {"requests": [{"updateCells": {
        "range": {"sheetId": sid, "startRowIndex": r - 1, "endRowIndex": r,
                  "startColumnIndex": _col(c), "endColumnIndex": _col(c) + 1},
        "rows": [{"values": [{"note": str(note)}]}],
        "fields": "note",
    }}]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Note added to {a1}."


@mcp.tool()
def delete_sheet_tab(
    spreadsheet_id: str,
    sheet: str,
    ctx: Context = None,
) -> str:
    """Delete a sheet tab by name. WARNING: destroys all data in it. """
    if not spreadsheet_id or not sheet:
        return "Error: spreadsheet_id and sheet are required"
    svc = _svc(ctx)
    sid = _sheet_id(svc, spreadsheet_id, sheet)
    if sid is None:
        return f"Error: sheet '{sheet}' not found"
    body = {"requests": [{"deleteSheet": {"sheetId": sid}}]}
    svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
    return f"Deleted sheet tab '{sheet}'."


@mcp.tool()
def get_spreadsheet_thumbnail(
    spreadsheet_id: str,
    ctx: Context = None,
) -> str:
    """Get the thumbnail URL of a spreadsheet (via Drive API). Returns the URL
    or an error if unavailable."""
    if not spreadsheet_id:
        return "Error: spreadsheet_id is required"
    svc = _svc(ctx)
    drive = ctx.request_context.lifespan_context.drive_service
    try:
        f = drive.files().get(fileId=spreadsheet_id,
                              fields="thumbnailLink,name").execute()
        link = f.get("thumbnailLink") or "No thumbnail available"
        return f"Thumbnail for '{f.get('name', spreadsheet_id)}': {link}"
    except Exception as e:
        return f"Error getting thumbnail: {str(e)[:120]}"


# ── Upload image to a spreadsheet cell ──────────────────────────────

def _upload_to_catbox(image_path, fname="image.png", timeout=30):
    """Upload a local image to catbox.moe; returns public URL or None."""
    import urllib.request
    global _catbox_cache
    boundary = "----GoClawCatbox"
    with open(image_path, "rb") as f:
        data = f.read()
    body = (
        f"--{boundary}\r\n"
        "Content-Disposition: form-data; name=\"reqtype\"\r\n\r\n"
        "fileupload\r\n"
        f"--{boundary}\r\n"
        f"Content-Disposition: form-data; name=\"fileToUpload\"; filename=\"{fname}\"\r\n"
        "Content-Type: image/png\r\n\r\n"
    ).encode("utf-8") + data + f"\r\n--{boundary}--\r\n".encode()
    req = urllib.request.Request("https://catbox.moe/user/api.php", data=body, method="POST")
    req.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
    req.add_header("User-Agent", "Mozilla/5.0")
    try:
        r = urllib.request.urlopen(req, timeout=timeout)
        url = r.read().decode().strip()
        if url.startswith("http"):
            return url
    except Exception:
        pass
    return None


@mcp.tool()
def upload_image_to_sheet(
    spreadsheet_id: str,
    image_path: str,
    cell: str = None,
    mode: str = "formula",
    width: int = 200,
    height: int = 200,
    sheet: str = None,
    ctx: Context = None,
) -> str:
    """Upload a local image file INTO a Google Sheet.

    - mode='formula' (default): inserts =IMAGE("<url>",1,w,h) into `cell`
      (A1 notation, e.g. 'E2'). Image lives INSIDE the cell, scales with it,
      prints nicely. `width`/`height` are the display size in px (default 200).
    - mode='overlay': floats the image over the sheet at `cell` origin via
      Sheets API overlayPosition (exact pixel size).

    The image is first uploaded to Google Drive (service account) and made
    readable without login, so Sheets can render it. Returns the Drive URL +
    where it was placed.
    """
    import os as _os
    if not spreadsheet_id or not image_path:
        return "Error: spreadsheet_id and image_path are required"
    if not _os.path.exists(image_path):
        return f"Error: file not found: {image_path}"
    svc = _svc(ctx)
    drive = ctx.request_context.lifespan_context.drive_service

    # 1. Upload into the shared "goclaw-mcp" Drive folder (owner: user). SA
    #    has Writer on it, so files live in the USER's Drive quota (not the
    #    0-limit SA quota). Falls back to the env folder id if configured.
    import os as _os
    folder_id = _os.environ.get("GOOGLE_DRIVE_FOLDER_ID", "").strip()

    mime = "image/png"
    ext = _os.path.splitext(image_path)[1].lower()
    mime_map = {".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
                ".gif": "image/gif", ".webp": "image/webp"}
    mime = mime_map.get(ext, mime)
    fname = _os.path.basename(image_path)
    # Host chooser: try catbox.moe first (no key, public link). If it fails,
    # fall back to the shared Drive folder (metadata-only won't work for
    # media content, so Drive is only attempted as a last resort and usually
    # fails due to SA quota 0 — the catbox path is the primary mechanism).
    url = _upload_to_catbox(image_path, fname="image.png")
    if not url:
        try:
            up = drive.files().create(
                body={"name": fname, "mimeType": mime,
                      "parents": [folder_id]},
                media_body=image_path, fields="id,name").execute()
            fid = up["id"]
            drive.permissions().create(fileId=fid, body={
                "type": "anyone", "role": "reader"}).execute()
            url = f"https://drive.google.com/uc?export=view&id={fid}"
        except Exception:
            return "Error: image upload failed (catbox unavailable & Drive SA has no media quota)."

    if mode == "overlay":
        if not cell:
            return "Error: cell required for overlay mode"
        import re as _re
        m = _re.match(r"^([A-Z]+)(\d+)$", cell.strip())
        if not m:
            return f"Error: cell must be like 'E2'"
        c, r = m.group(1), int(m.group(2))
        def _col(x):
            n = 0
            for ch in x:
                n = n * 26 + (ord(ch.upper()) - 64)
            return n - 1
        sid = _sheet_id(svc, spreadsheet_id, sheet)
        if sid is None:
            return f"Error: sheet '{sheet}' not found"
        body = {"requests": [{"updateCells": {
            "range": {"sheetId": sid, "startRowIndex": r - 1, "endRowIndex": r,
                      "startColumnIndex": _col(c), "endColumnIndex": _col(c) + 1},
            "rows": [{"values": [{
                "userEnteredImage": {"imageUrl": url,
                                     "size": {"width": {"value": int(width)},
                                              "height": {"value": int(height)}}},
            }]}],
            "fields": "userEnteredImage",
        }}]}
        svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
        return f"Uploaded {fname} and placed overlay at {cell}. URL: {url[:80]}"
    else:
        if not cell:
            return "Error: cell required for formula mode"
        # IMAGE() arg rules:
        #   mode 1 (fit) = IMAGE(url, 1) — no size args allowed;
        #   custom size = IMAGE(url, 4, width, height) — px, deterministic.
        # Default to mode 4 with the requested size so the image is NOT tiny.
        import re as _re
        formula = f'=IMAGE("{url}", 4, {int(height)}, {int(width)})'
        # auto-row-height so the resized image is visible.
        try:
            col_l, row_n = _re.match(r"^([A-Z]+)(\d+)$", cell.strip()).groups()
            def _col(x):
                n = 0
                for ch in x:
                    n = n * 26 + (ord(ch.upper()) - 64)
                return n - 1
            sid = _sheet_id(svc, spreadsheet_id, sheet)
            body = {"requests": [{
                "updateDimensionProperties": {
                    "range": {"sheetId": sid, "dimension": "ROWS",
                              "startIndex": int(row_n) - 1, "endIndex": int(row_n)},
                    "properties": {"pixelSize": int(height) + 20},
                    "fields": "pixelSize",
                }
            }, {
                "updateDimensionProperties": {
                    "range": {"sheetId": sid, "dimension": "COLUMNS",
                              "startIndex": _col(col_l), "endIndex": _col(col_l) + 1},
                    "properties": {"pixelSize": int(width) + 20},
                    "fields": "pixelSize",
                }
            }]}
            svc.spreadsheets().batchUpdate(spreadsheetId=spreadsheet_id, body=body).execute()
        except Exception:
            pass  # resize is best-effort
        r = svc.spreadsheets().values().update(
            spreadsheetId=spreadsheet_id, range=f"{sheet or 'Sheet1'}!{cell}",
            valueInputOption="USER_ENTERED",
            body={"values": [[formula]]}).execute()
        return (f"Uploaded {fname} to hosting, set =IMAGE at "
                f"{cell} ({r.get('updatedRange','')}). URL: {url[:80]}")


# ── Override batch_update with request sanitizer ────────────────────
# The upstream tool forwards the model's list verbatim; LLMs frequently add
# advisory fields ("reason", "note") which Google rejects with HTTP 400
# "Unknown name". Remove the upstream registration, then re-register the
# hardened version under the same name.
try:
    mcp.remove_tool("batch_update")
except Exception:
    pass


@mcp.tool(name="batch_update")
def batch_update_safe(
    spreadsheet_id: str,
    requests: list,
    ctx: Context = None,
) -> Dict[str, Any]:
    """Execute a batchUpdate with all Sheets API request types (addSheet,
    updateCells, repeatCell, updateBorders, insertDimension, conditional
    format, protected ranges, charts, ...).

    The `requests` list is sanitized before sending: explanatory keys such as
    "reason", "note" or "explanation" are removed so Google does not reject
    the payload with "Invalid JSON payload / Unknown name".

    Args:
        spreadsheet_id: spreadsheet id
        requests: list of batchUpdate request objects, e.g.
            [{"repeatCell": {"range": {...}, "cell": {...}, "fields": "..."}}]
    """
    if not spreadsheet_id:
        return {"error": "spreadsheet_id is required"}
    clean, note = _sanitize_requests(requests)
    if not clean:
        return {"error": "no valid batchUpdate operation found in requests",
                "hint": "each item must contain one of: " +
                        ", ".join(sorted(list(_BATCH_OPS))[:12]) + " ..."}
    svc = _svc(ctx)
    body = {"requests": clean}
    resp = svc.spreadsheets().batchUpdate(
        spreadsheetId=spreadsheet_id, body=body).execute()
    replies = resp.get("replies", [])
    return {"ok": True, "replies": replies,
            "message": f"Applied {len(clean)} request(s)." + note}


# ── Start the original MCP server (unchanged) ───────────────────────
mcp.run(transport="sse")