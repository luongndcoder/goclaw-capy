package zalo

import (
	"strings"
	"testing"
)

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain text", "hello world", "hello world"},

		// Bold & italic
		{"bold stars", "this is **bold** text", "this is bold text"},
		{"bold underscores", "this is __bold__ text", "this is bold text"},
		{"bold+italic stars", "***important***", "important"},
		{"strikethrough", "this is ~~deleted~~ text", "this is deleted text"},

		// Code
		{"inline code", "use `fmt.Println` here", "use fmt.Println here"},
		{"fenced code block", "before\n```go\nfmt.Println(\"hi\")\n```\nafter", "before\nfmt.Println(\"hi\")\n\nafter"},
		{"fenced code block no lang", "```\ncode here\n```", "code here"},

		// Links & images
		{"link", "click [here](https://example.com) now", "click here (https://example.com) now"},
		{"image", "see ![alt](https://img.png) below", "see  below"},

		// Headers
		{"h1", "# Title", "Title"},
		{"h3", "### Section", "Section"},
		{"h6", "###### Deep", "Deep"},

		// Horizontal rules
		{"hr dashes", "above\n---\nbelow", "above\n\nbelow"},
		{"hr stars", "above\n***\nbelow", "above\n\nbelow"},

		// Blockquotes
		{"blockquote", "> this is quoted\n> second line", "this is quoted\nsecond line"},
		{"nested blockquote", "> > deep", "> deep"},

		// Bullets
		{"dash bullet", "- item one\n- item two", "• item one\n• item two"},
		{"star bullet", "* item one\n* item two", "• item one\n• item two"},
		{"plus bullet", "+ item one", "• item one"},
		{"indented bullet", "list:\n  - nested item", "list:\n  • nested item"},

		// Excessive newlines
		{"excessive newlines", "a\n\n\n\nb", "a\n\nb"},

		// Mixed
		{"mixed markdown", "## Hello\n\nThis is **bold** and `code`.\n\n- item\n- [link](url)\n\n> quote", "Hello\n\nThis is bold and code.\n\n• item\n• link (url)\n\nquote"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMarkdown(tt.in)
			if got != tt.want {
				t.Errorf("StripMarkdown(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripMarkdown_TablesBecomeLines(t *testing.T) {
	in := "Báo cáo nợ:\n\n| Thành viên | Số tiền |\n|---|---|\n| Chị Linh | 1040000 |\n| Anh Tuấn | 500000 |\n\nHết."
	got := StripMarkdown(in)
	for _, keep := range []string{"Báo cáo nợ:", "Thành viên: Chị Linh, Số tiền: 1040000", "Thành viên: Anh Tuấn, Số tiền: 500000", "Hết."} {
		if !strings.Contains(got, keep) {
			t.Errorf("missing %q in:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "|") {
		t.Errorf("pipe characters leaked:\n%s", got)
	}
}

func TestStripMarkdown_LonePipeLineUntouched(t *testing.T) {
	in := "Ghi chú | mang theo tiền mặt"
	if got := StripMarkdown(in); got != in {
		t.Errorf("lone pipe line modified: %q", got)
	}
}

func TestStripMarkdown_HTMLStripped(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Chào <b>anh Lương</b> nhé", "Chào anh Lương nhé"},
		{"Xem <a href=\"https://x.vn/qr\">mã QR</a> giúp em", "Xem mã QR (https://x.vn/qr) giúp em"},
		{"Kết quả:<br/>xong", "Kết quả:xong"},
		{"5 < 10 và a > b", "5 < 10 và a > b"},
		{"Dùng <pre>code</pre> nhé", "Dùng code nhé"},
	}
	for _, c := range cases {
		if got := StripMarkdown(c.in); got != c.want {
			t.Errorf("StripMarkdown(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
