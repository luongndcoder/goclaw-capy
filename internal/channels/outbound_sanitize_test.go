package channels

import (
	"strings"
	"testing"
)

func TestSanitizeOutbound(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		notContain []string
		contain    []string
	}{
		{
			name:       "clean message untouched",
			in:         "BẢNG QUẢN LÝ CÔNG NỢ\n\n1. Anh Hiển (Bông) — 500,000 ₫ — Chưa thanh toán",
			contain:    []string{"BẢNG QUẢN LÝ CÔNG NỢ", "500,000"},
			notContain: nil,
		},
		{
			name:       "image formula becomes bare url",
			in:         `ô QR: =IMAGE("https://api.vietqr.io/image/970441-014850652-QRIBFTT.jpg?accountName=X&amount=387000", 4, 150, 150) nhé`,
			contain:    []string{"https://api.vietqr.io/image/970441-014850652-QRIBFTT.jpg?accountName=X&amount=387000"},
			notContain: []string{"=IMAGE("},
		},
		{
			name:       "html escaped image formula",
			in:         `=IMAGE(&quot;https://example.com/qr.png&quot;, 4, 150, 150)`,
			contain:    []string{"https://example.com/qr.png"},
			notContain: []string{"=IMAGE(", "&quot;"},
		},
		{
			name: "credentialed exec dump keeps data drops diagnostics",
			in: `[CREDENTIALED EXEC] Command failed (exit code 3).
Binary: /usr/local/bin/gws
Args: sheets +read --spreadsheet ABC --range Sheet1!A1:I15
Note: This runs in Direct Exec Mode — shell operators are NOT supported.
{
  "error": {
    "code": 400,
    "message": "unexpected argument",
    "reason": "validationError"
  }
}
STDERR:
error[validation]: error: unexpected argument
Usage: gws sheets +read [SPREADSHEET] [RANGE]
For more information, try '--help'.
[
  ["BẢNG QUẢN LÝ CÔNG NỢ"]
]`,
			contain:    []string{"BẢNG QUẢN LÝ CÔNG NỢ"},
			notContain: []string{"[CREDENTIALED EXEC]", "Binary:", "Args:", "validationError", "Usage:", "STDERR:", `"reason"`},
		},
		{
			name:       "pure dump becomes fallback notice",
			in:         "[CREDENTIALED EXEC] Command failed (exit code 3).\nBinary: /usr/local/bin/gws\nUsage: gws sheets +read [SPREADSHEET] [RANGE]",
			contain:    []string{"trục trặc"},
			notContain: []string{"[CREDENTIALED EXEC]", "Binary:", "Usage:"},
		},
		{
			name:       "stack traces removed",
			in:         "result ok\n    at fetch (/app/index.js:10:5)\ngoroutine 1 [running]:\nTraceback (most recent call last):\n  File \"x.py\", line 3",
			contain:    []string{"result ok"},
			notContain: []string{"at fetch", "goroutine", "Traceback", "line 3"},
		},
		{
			name:       "empty stays empty",
			in:         "",
			contain:    nil,
			notContain: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeOutbound(tc.in)
			if tc.in == "" && got != "" {
				t.Fatalf("empty input must stay empty, got %q", got)
			}
			for _, want := range tc.contain {
				if !strings.Contains(got, want) {
					t.Errorf("expected output to contain %q, got:\n%s", want, got)
				}
			}
			for _, banned := range tc.notContain {
				if strings.Contains(got, banned) {
					t.Errorf("expected output NOT to contain %q, got:\n%s", banned, got)
				}
			}
		})
	}
}
