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

	// 2b. Fenced code blocks that are pasted tool output (MCP dumps, skill
	// files, tracebacks, shell sessions) are removed entirely. Genuine code
	// answers the user asked for are kept.
	if fenceRe.MatchString(text) {
		text = fenceRe.ReplaceAllStringFunc(text, func(fence string) string {
			if isToolOutputBody(fence) {
				redactions++
				return ""
			}
			return fence
		})
	}

	// 2c. Skill frontmatter dumps (--- name: ... description: ... ---).
	if skillFrontmatterRe.MatchString(text) {
		text = skillFrontmatterRe.ReplaceAllString(text, "")
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

		// 3b2. Standalone technical echo lines, regardless of exec context:
		// tool dump banners, table/row dumps, shell prompts, tool-call echoes.
		if toolEchoLineRe.MatchString(ln) {
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

	// Fenced code blocks — body inspected by isToolOutputBody before removal.
	fenceRe = regexp.MustCompile("(?s)```[a-zA-Z]*\\n.*?```")

	// Skill frontmatter dumps pasted from SKILL.md files.
	skillFrontmatterRe = regexp.MustCompile(`(?s)^---\nname: .+\ndescription: .*?\n---`)

	// Standalone technical echo lines (no exec context required): MCP dump
	// banners, table/row dumps, shell prompts, tool-call echoes, bare
	// failure summaries. Natural chat text does not look like this.
	toolEchoLineRe = regexp.MustCompile(`(?i)^\s*(?:` +
		`=== .+ ===` + // MCP dump banners: === ALL TABLES IN DOC ... ===
		`|Table \d+\s+(?:rows|sample|columns).*$` + // Table 1 rows: 156
		`|Row \d+\s*:.*` + // Row 0: [...]
		`|\$\s+(?:gws|docker|kubectl|curl|wget|python3?|pip3?|npm|pnpm|node|go|git|ssh|cd|ls|cat|echo|export|sudo|bash|sh|chmod|chown|mkdir|rm|cp|mv|tar|grep|awk|sed|find|ps|kill|systemctl|service|npx|tsx|uv|make)\b.*` + // shell prompts: $ gws ...
		`|(?:tool(?:\s+call)?|arguments)\s*:.*` + // Tool: x / Arguments: {...}
		`|exit code:?\s*\d+.*` +
		`|command (?:failed|not found|timed out).*` +
		`)\s*$`)

	blankRunRe = regexp.MustCompile(`\n{3,}`)
)

func jsonErrorAhead(lines []string, i int) bool {
	depth := 0
	for j := i; j < len(lines) && j < i+25; j++ {
		lower := strings.ToLower(lines[j])
		if strings.Contains(lower, `"reason"`) || strings.Contains(lower, `"error"`) || strings.Contains(lower, `"code"`) || strings.Contains(lower, `"spreadsheetid"`) ||
			strings.Contains(lower, `"valueranges"`) || strings.Contains(lower, `"majordimension"`) || strings.Contains(lower, `"namedranges"`) {
			return true
		}
		depth += strings.Count(lines[j], "{") + strings.Count(lines[j], "[") - strings.Count(lines[j], "}") - strings.Count(lines[j], "]")
		if depth <= 0 && j > i {
			return false
		}
	}
	return false
}

// isToolOutputBody reports whether a fenced code block body looks like pasted
// tool/service output rather than a code answer: MCP envelopes and dump keys,
// table/row dumps, tracebacks, exec markers, shell prompts, skill frontmatter.
func isToolOutputBody(fence string) bool {
	lower := strings.ToLower(fence)
	signatures := []string{
		"external_untrusted_content", "spreadsheetid", "valueranges",
		"majordimension", "table 1 rows", "table 0 rows",
		"traceback (most recent call last)", "goroutine ",
		"[credentialed exec]", "stderr:",
	}
	for _, s := range signatures {
		if strings.Contains(lower, s) {
			return true
		}
	}
	// Skill frontmatter pair inside a fence (name: + description: together).
	if strings.Contains(lower, "name: ") && strings.Contains(lower, "description: ") {
		return true
	}
	for _, re := range []*regexp.Regexp{toolEchoLineRe, stackTraceLineRe} {
		if re.MatchString(fence) {
			return true
		}
	}
	return false
}
