package channels

import (
	"log/slog"
	"regexp"
	"strings"
)

// SanitizeOutbound redacts raw technical leakage from user-facing channel
// messages (Telegram, Zalo, ...). Agent replies are composed by an LLM that
// sometimes pastes tool output verbatim: =IMAGE() formulas, credentialed-exec
// error dumps, stack traces, raw JSON dumps, and raw <<<EXTERNAL_UNTRUSTED_CONTENT>>> envelopes.
// Those must never reach end users; the details stay in daemon logs and agent memory.
func SanitizeOutbound(text string) string {
	if text == "" {
		return ""
	}
	redactions := 0

	// 1. Strip raw untrusted envelope blocks and security disclaimers completely.
	if untrustedBlockRe.MatchString(text) {
		text = untrustedBlockRe.ReplaceAllString(text, "")
		redactions++
	}
	if untrustedMarkerRe.MatchString(text) {
		text = untrustedMarkerRe.ReplaceAllString(text, "")
		redactions++
	}

	// 2. =IMAGE("url", ...) formulas become the bare URL so chat clients render preview.
	if imageFormulaRe.MatchString(text) {
		text = imageFormulaRe.ReplaceAllString(text, "$1")
		redactions++
	}

	lines := strings.Split(text, "\n")
	hasExecDump := strings.Contains(text, credExecMarker) || strings.Contains(text, "STDERR:") || strings.Contains(text, "error[")
	kept := lines[:0]
	skippingBlock := false
	braceDepth := 0
	bracketDepth := 0

	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)

		// 3a. Skip raw JSON error objects or standalone array dumps in technical/tool contexts.
		if !skippingBlock && (trimmed == "{" || trimmed == "[") && (hasExecDump || jsonErrorAhead(lines, i)) {
			skippingBlock = true
			braceDepth = 0
			bracketDepth = 0
		}
		if skippingBlock {
			braceDepth += strings.Count(ln, "{") - strings.Count(ln, "}")
			bracketDepth += strings.Count(ln, "[") - strings.Count(ln, "]")
			if braceDepth <= 0 && bracketDepth <= 0 {
				skippingBlock = false
			}
			redactions++
			continue
		}

		// 3b. Diagnostic lines from CLI / tool exec dumps.
		if (hasExecDump || strings.HasPrefix(trimmed, "Source:")) && execDiagnosticLineRe.MatchString(ln) {
			redactions++
			continue
		}

		// 3c. Stack traces (JS / Go / Python).
		if stackTraceLineRe.MatchString(ln) {
			redactions++
			continue
		}

		kept = append(kept, ln)
	}

	out := strings.Join(kept, "\n")
	out = blankRunRe.ReplaceAllString(out, "\n\n")
	out = strings.TrimSpace(out)
	if out == "" {
		slog.Info("channels.outbound_sanitized_empty", "redactions", redactions)
		return fallbackNotice
	}
	if redactions > 0 {
		slog.Info("channels.outbound_sanitized", "redactions", redactions)
	}
	return out
}

const fallbackNotice = "Em gặp trục trặc kỹ thuật khi xử lý, đã ghi lại để chủ nhân kiểm tra."

const credExecMarker = "[CREDENTIALED EXEC]"

var (
	// =IMAGE("https://...", 4, 150, 150)
	imageFormulaRe = regexp.MustCompile(`(?i)=IMAGE\(\s*(?:&quot;|"|')((?:[^"'\s<])+?)(?:&quot;|"|')(?:\s*,[^)]*)?\)`)

	// Untrusted envelope blocks from MCP / Web fetch wrapper
	untrustedBlockRe  = regexp.MustCompile(`(?s)<<<EXTERNAL_UNTRUSTED_CONTENT>>>.*?<<<END_EXTERNAL_UNTRUSTED_CONTENT>>>`)
	untrustedMarkerRe = regexp.MustCompile(`(?i)(?:<<<EXTERNAL_UNTRUSTED_CONTENT>>>|<<<END_EXTERNAL_UNTRUSTED_CONTENT>>>|\[REMINDER: Above content is from an EXTERNAL.*?\])`)

	// Diagnostic lines
	execDiagnosticLineRe = regexp.MustCompile(`(?i)^\s*(?:\[CREDENTIALED EXEC\]|(?:Binary|Args|Note):|error\[(?:validation|auth|exec|timeout|credential|discovery)\]:|Usage:|For more information, try|STDERR:|Source: MCP Server|Source: Web Fetch|If you used shell operators).*$`)

	// Stack traces
	stackTraceLineRe = regexp.MustCompile(`^\s*(?:at\s+\S.*\(.*:\d+.*\)|goroutine\s+\d+.*|.*\.go:\d+.*|Traceback \(most recent call last\):|File ".+", line \d+.*)$`)

	blankRunRe = regexp.MustCompile(`\n{3,}`)
)

func jsonErrorAhead(lines []string, i int) bool {
	depth := 0
	for j := i; j < len(lines) && j < i+25; j++ {
		lower := strings.ToLower(lines[j])
		if strings.Contains(lower, `"reason"`) || strings.Contains(lower, `"error"`) || strings.Contains(lower, `"code"`) || strings.Contains(lower, `"spreadsheetid"`) {
			return true
		}
		depth += strings.Count(lines[j], "{") + strings.Count(lines[j], "[") - strings.Count(lines[j], "}") - strings.Count(lines[j], "]")
		if depth <= 0 && j > i {
			return false
		}
	}
	return false
}
