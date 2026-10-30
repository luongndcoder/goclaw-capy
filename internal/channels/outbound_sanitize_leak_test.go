package channels

import (
	"strings"
	"testing"
)

// TestSanitizeOutbound_MCPDocsDump is built from a real leak seen on the home
// VPS: a docs-MCP table dump with no envelope and no error keys passed the
// old filter untouched and reached the user.
func TestSanitizeOutbound_MCPDocsDump(t *testing.T) {
	in := "Dạ đây anh:\n" +
		"=== ALL TABLES IN DOC 1SCYVXGKxEwGSRKfYfl-7lxZcipl8Bj-fl18GUSc2zCk ===\n" +
		"Table 1 rows: 156\n" +
		"Table 1 sample header: ['STT', 'Danh sách học sinh']\n" +
		"Table 2 rows: 22\n" +
		"Row 0: ['Thứ', 'Khung giờ', 'Lớp học']\n" +
		"Row 1: ['Thứ 2', '17:30 - 19:00', 'snowwhite 1']\n" +
		"Em tổng hợp xong rồi nhé!"

	got := SanitizeOutbound(in)
	for _, leak := range []string{"=== ALL TABLES", "Table 1 rows", "Row 0:", "Row 1:", "snowwhite"} {
		if strings.Contains(got, leak) {
			t.Errorf("dump line leaked: %q\nfull output:\n%s", leak, got)
		}
	}
	for _, keep := range []string{"Dạ đây anh:", "Em tổng hợp xong rồi nhé!"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost natural text %q, got:\n%s", keep, got)
		}
	}
}

// TestSanitizeOutbound_ShellAndToolEchoes covers command lines and tool-call
// echoes pasted by the model.
func TestSanitizeOutbound_ShellAndToolEchoes(t *testing.T) {
	in := "Em chạy lệnh này:\n" +
		"$ gws sheets read --id abc\n" +
		"Tool: mcp_sheets__get_sheet_data\n" +
		"Arguments: {\"spreadsheet_id\": \"abc\"}\n" +
		"exit code: 0\n" +
		"Kết quả là 5 dòng."
	got := SanitizeOutbound(in)
	for _, leak := range []string{"$ gws", "Tool: mcp", "Arguments:", "exit code"} {
		if strings.Contains(got, leak) {
			t.Errorf("technical echo leaked: %q\nfull output:\n%s", leak, got)
		}
	}
	if !strings.Contains(got, "Kết quả là 5 dòng.") {
		t.Errorf("lost natural text, got:\n%s", got)
	}
}

// TestSanitizeOutbound_FencedDumpVsCode keeps genuine code answers while
// removing fenced tool output.
func TestSanitizeOutbound_FencedDumpVsCode(t *testing.T) {
	dump := "Kết quả:\n```json\n{\"spreadsheetId\": \"abc\", \"valueRanges\": []}\n```\nXong nhé!"
	if got := SanitizeOutbound(dump); strings.Contains(got, "spreadsheetId") {
		t.Errorf("fenced MCP dump leaked:\n%s", got)
	}

	code := "Anh dùng hàm này nhé:\n```python\ndef tong(a, b):\n    return a + b\n```\nEm giải thích: hàm cộng hai số."
	got := SanitizeOutbound(code)
	if !strings.Contains(got, "def tong") {
		t.Errorf("genuine code answer was stripped:\n%s", got)
	}

	skill := "Đây ạ:\n```markdown\n---\nname: gws-tenant-routing\ndescription: \"Google Workspace via gws.\"\n---\n# Skill\n```\nHết."
	if got := SanitizeOutbound(skill); strings.Contains(got, "gws-tenant-routing") {
		t.Errorf("fenced skill dump leaked:\n%s", got)
	}
}

// TestSanitizeOutbound_MoneyTextUntouched guards against over-stripping:
// prices and markdown tables in normal answers must survive.
func TestSanitizeOutbound_MoneyTextUntouched(t *testing.T) {
	in := "Tổng là $ 100 phí ship.\n\n| Mặt hàng | Giá |\n|---|---|\n| Sách | 50k |"
	got := SanitizeOutbound(in)
	for _, keep := range []string{"$ 100", "Mặt hàng", "Sách"} {
		if !strings.Contains(got, keep) {
			t.Errorf("over-stripped natural text %q, got:\n%s", keep, got)
		}
	}
}

// TestSanitizeOutbound_GenericServerDump proves the rules are structural, not
// per-server: a dump from a hypothetical future MCP server with unknown keys
// is stripped all the same.
func TestSanitizeOutbound_GenericServerDump(t *testing.T) {
	in := "Kết quả tra cứu:\n" +
		"```json\n" +
		"{\n" +
		"  \"customer_ref\": \"ACME-8891\",\n" +
		"  \"ledger_entries\": [\n" +
		"    {\"id\": 1, \"amount\": 500},\n" +
		"    {\"id\": 2, \"amount\": 700}\n" +
		"  ],\n" +
		"  \"cursor\": \"next-page-token-xyz\"\n" +
		"}\n" +
		"```\n" +
		"Tóm lại còn 2 khoản."
	got := SanitizeOutbound(in)
	for _, leak := range []string{"customer_ref", "ledger_entries", "ACME-8891"} {
		if strings.Contains(got, leak) {
			t.Errorf("unknown-server dump leaked: %q\nfull output:\n%s", leak, got)
		}
	}
	if !strings.Contains(got, "Tóm lại còn 2 khoản.") {
		t.Errorf("lost natural text, got:\n%s", got)
	}

	// Same payload unfenced must go too.
	plain := "Đây nhé:\n{\n  \"customer_ref\": \"ACME-8891\",\n  \"ledger_entries\": [],\n  \"cursor\": \"tok\"\n}\nHết."
	if got := SanitizeOutbound(plain); strings.Contains(got, "ACME-8891") {
		t.Errorf("unfenced unknown-server dump leaked:\n%s", got)
	}

	// Small illustrative snippets stay.
	small := "Ví dụ payload: {\"id\": 1} là như vậy."
	if got := SanitizeOutbound(small); !strings.Contains(got, "{\"id\": 1}") {
		t.Errorf("small snippet wrongly stripped:\n%s", got)
	}
}
