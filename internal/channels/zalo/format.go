package zalo

import (
	"regexp"
	"strings"
)

// StripMarkdown removes markdown formatting artifacts from text, producing
// clean plain text suitable for Zalo which does not support any markup.
func StripMarkdown(text string) string {
	if text == "" {
		return text
	}

	// 0. Tables → readable lines first (while pipes are intact): Zalo has no
	// monospace, so aligned-pipe tables arrive as walls of text on phones.
	text = renderTablesAsLines(text)

	// 0b. HTML tags (the model sometimes emits Telegram-style markup shared
	// across channels): links keep "text (url)", all other tags drop leaving
	// inner text. Plain comparisons like "5 < 10" are untouched.
	text = reHTMLLink.ReplaceAllString(text, "$2 ($1)")
	text = reHTMLTag.ReplaceAllString(text, "")

	// 1. Strip fenced code blocks — keep content, remove ``` delimiters
	text = reFencedCode.ReplaceAllString(text, "$1")

	// 2. Strip inline code backticks
	text = reInlineCode.ReplaceAllString(text, "$1")

	// 3. Strip images ![alt](url) — remove entirely
	text = reImage.ReplaceAllString(text, "")

	// 4. Strip links [text](url) → text (url)
	text = reLink.ReplaceAllString(text, "$1 ($2)")

	// 5. Strip bold+italic (***text*** or ___text___)
	text = reBoldItalicStar.ReplaceAllString(text, "$1")
	text = reBoldItalicUnder.ReplaceAllString(text, "$1")

	// 6. Strip bold (**text** or __text__)
	text = reBoldStar.ReplaceAllString(text, "$1")
	text = reBoldUnder.ReplaceAllString(text, "$1")

	// 7. Strip strikethrough ~~text~~
	text = reStrikethrough.ReplaceAllString(text, "$1")

	// 8. Strip headers (lines starting with #)
	text = reHeader.ReplaceAllString(text, "$1")

	// 9. Strip horizontal rules
	text = reHorizontalRule.ReplaceAllString(text, "")

	// 10. Strip blockquotes
	text = reBlockquote.ReplaceAllString(text, "$1")

	// 11. Replace bullet markers with •
	text = reBullet.ReplaceAllString(text, "${1}• ")

	// Clean up excessive blank lines (3+ → 2)
	text = reExcessiveNewlines.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}

var (
	reFencedCode      = regexp.MustCompile("(?s)```[a-zA-Z0-9]*\\n?(.*?)```")
	reInlineCode      = regexp.MustCompile("`([^`]+)`")
	reImage           = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	reLink            = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reBoldItalicStar  = regexp.MustCompile(`\*{3}(.+?)\*{3}`)
	reBoldItalicUnder = regexp.MustCompile(`_{3}(.+?)_{3}`)
	reBoldStar        = regexp.MustCompile(`\*{2}(.+?)\*{2}`)
	reBoldUnder       = regexp.MustCompile(`_{2}(.+?)_{2}`)
	reStrikethrough   = regexp.MustCompile(`~~(.+?)~~`)
	reHeader          = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	reHorizontalRule  = regexp.MustCompile(`(?m)^[\s]*[-*_]{3,}[\s]*$`)
	reBlockquote      = regexp.MustCompile(`(?m)^>\s?(.*)$`)
	reBullet          = regexp.MustCompile(`(?m)^(\s*)[-*+]\s+`)

	reExcessiveNewlines = regexp.MustCompile(`\n{3,}`)

	// HTML from cross-channel markup: <a href="u">t</a> → "t (u)".
	reHTMLLink = regexp.MustCompile(`(?i)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	// Any other tag <b>, <pre>, <br/> … → dropped, inner text kept.
	// Requires a letter right after < so "a < b" comparisons survive.
	reHTMLTag = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
)

// tableSeparatorRe matches a markdown table delimiter row: |---|---|.
var tableSeparatorRe = regexp.MustCompile(`^\s*\|?[\s:|\-]+\|\s*$`)

// splitTableRow splits a "| a | b |" line into trimmed cells.
func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// renderTablesAsLines converts markdown table blocks into one bullet line per
// data row ("• H1: v1, H2: v2"). Only blocks with a header + delimiter row
// convert; lone pipe lines pass through untouched.
func renderTablesAsLines(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		// A bare delimiter row outside a converted table is never prose.
		if tableSeparatorRe.MatchString(lines[i]) && strings.Contains(lines[i], "-") {
			i++
			continue
		}
		if i+1 < len(lines) && isTableHeaderLine(lines[i]) && tableSeparatorRe.MatchString(lines[i+1]) &&
			strings.Contains(lines[i+1], "-") {
			header := splitTableRow(lines[i])
			j := i + 2
			converted := 0
			for j < len(lines) && isTableRowLine(lines[j]) {
				row := splitTableRow(lines[j])
				pairs := make([]string, 0, len(header))
				for k := range header {
					if header[k] == "" {
						continue
					}
					v := ""
					if k < len(row) {
						v = row[k]
					}
					pairs = append(pairs, header[k]+": "+v)
				}
				if len(pairs) > 0 {
					out = append(out, "• "+strings.Join(pairs, ", "))
					converted++
				}
				j++
			}
			if converted > 0 {
				i = j
				continue
			}
		}
		out = append(out, lines[i])
		i++
	}
	return strings.Join(out, "\n")
}

// isTableHeaderLine reports a plausible table header row: pipe-delimited with
// at least 2 cells.
func isTableHeaderLine(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.Contains(t, "|") {
		return false
	}
	return len(splitTableRow(line)) >= 2
}

// isTableRowLine reports a plausible table data row: starts with a pipe.
func isTableRowLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}
