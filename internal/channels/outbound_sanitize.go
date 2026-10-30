package channels

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
)

// SanitizeOutbound redacts raw technical leakage from user-facing channel
// messages (Telegram, Zalo, ...). Agent replies are composed by an LLM that
// sometimes pastes tool/service output verbatim: data dumps, shell sessions,
// tool-call echoes, stack traces, and raw <<<EXTERNAL_UNTRUSTED_CONTENT>>> envelopes.
//
// Design principle: rules match GENERIC machine-output shapes (balanced data
// blocks, dump banners, indexed-row serialization, shell prompts, harness
// echoes) — never literals from one specific tool or MCP server. A new server
// whose output has the same shapes is covered without new rules. The details
// stay in daemon logs and agent memory; chat stays natural language.
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

	// 2b. Fenced blocks that are pasted machine output are removed entirely.
	// Genuine code answers the user asked for are kept.
	if fenceRe.MatchString(text) {
		text = fenceRe.ReplaceAllStringFunc(text, func(fence string) string {
			if isMachineOutputBody(fenceBody(fence)) {
				redactions++
				return ""
			}
			return fence
		})
	}

	// 2c. YAML frontmatter dumps (--- key: ... key: ... ---) from pasted files.
	if frontmatterRe.MatchString(text) {
		text = frontmatterRe.ReplaceAllString(text, "")
		redactions++
	}

	lines := strings.Split(text, "\n")
	hasExecDump := strings.Contains(text, credExecMarker) || strings.Contains(text, "STDERR:") || strings.Contains(text, "error[")
	kept := lines[:0]
	skippingBlock := false
	braceDepth := 0
	bracketDepth := 0
	var blockBuf []string

	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)

		// 3a. Skip standalone balanced data blocks: generic error objects or
		// any data dump (parses as structured data and is too large to be
		// chat prose). Small illustrative snippets stay.
		if !skippingBlock && (trimmed == "{" || trimmed == "[") && (hasExecDump || dataBlockAhead(lines, i)) {
			skippingBlock = true
			braceDepth = 0
			bracketDepth = 0
			blockBuf = blockBuf[:0]
		}
		if skippingBlock {
			braceDepth += strings.Count(ln, "{") - strings.Count(ln, "}")
			bracketDepth += strings.Count(ln, "[") - strings.Count(ln, "]")
			blockBuf = append(blockBuf, ln)
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

		// 3b2. Standalone machine-echo lines, regardless of exec context:
		// dump banners, serialized rows, shell prompts, harness echoes.
		if machineEchoLineRe.MatchString(ln) {
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

// minDumpChars / minDumpLines bound what counts as a "data dump" versus a
// small illustrative snippet the model may legitimately show.
const (
	minDumpChars = 400
	minDumpLines = 3
)

var (
	// =IMAGE("https://...", 4, 150, 150)
	imageFormulaRe = regexp.MustCompile(`(?i)=IMAGE\(\s*(?:&quot;|"|')((?:[^"'\s<])+?)(?:&quot;|"|')(?:\s*,[^)]*)?\)`)

	// Untrusted envelope blocks from external tool wrappers
	untrustedBlockRe  = regexp.MustCompile(`(?s)<<<EXTERNAL_UNTRUSTED_CONTENT>>>.*?<<<END_EXTERNAL_UNTRUSTED_CONTENT>>>`)
	untrustedMarkerRe = regexp.MustCompile(`(?i)(?:<<<EXTERNAL_UNTRUSTED_CONTENT>>>|<<<END_EXTERNAL_UNTRUSTED_CONTENT>>>|\[REMINDER: Above content is from an EXTERNAL.*?\])`)

	// Diagnostic lines
	execDiagnosticLineRe = regexp.MustCompile(`(?i)^\s*(?:\[CREDENTIALED EXEC\]|(?:Binary|Args|Note):|error\[(?:validation|auth|exec|timeout|credential|discovery)\]:|Usage:|For more information, try|STDERR:|Source: MCP Server|Source: Web Fetch|If you used shell operators).*$`)

	// Stack traces
	stackTraceLineRe = regexp.MustCompile(`(?m)^\s*(?:at\s+\S.*\(.*:\d+.*\)|goroutine\s+\d+.*|.*\.go:\d+.*|Traceback \(most recent call last\):|File ".+", line \d+.*)$`)

	// Fenced code blocks — body inspected before removal.
	fenceRe = regexp.MustCompile("(?s)```[a-zA-Z]*\\n.*?```")

	// YAML frontmatter dumps pasted from files (--- key: ... ---).
	frontmatterRe = regexp.MustCompile(`(?ms)^---\n(?:[A-Za-z_][\w-]*: .+\n){2,}---`)

	// Generic error keys found in any tool's failure payload.
	genericErrorKeyRe = regexp.MustCompile(`(?i)"(?:error|reason|code|message|detail|traceback|exception)"\s*:`)

	// Indexed-row serialization runs (Row 0: [...], Table 1 rows: N) from any
	// tool that serializes tabular data as text.
	indexedRowRe = regexp.MustCompile(`(?i)^\s*(?:Row\s+\d+\s*:|Table\s+\d+\s+(?:rows|columns|sample)).*$`)

	// Standalone machine-echo lines (no exec context required): dump
	// banners, serialized rows, shell prompts, harness tool-call echoes, bare
	// failure summaries. Natural chat text does not look like this.
	// A shell prompt is "$" followed by a non-digit (amounts like "$ 100"
	// start with a digit and are kept) — generic shell, no verb list.
	machineEchoLineRe = regexp.MustCompile(`(?im)^\s*(?:` +
		`=== .+ ===` + // machine dump banners
		`|Row\s+\d+\s*:.*` + // serialized rows
		`|Table\s+\d+\s+(?:rows|columns|sample).*$` + // serialized table metadata
		`|\$\s+[A-Za-z_/.\-]\S*.*` + // shell prompts
		`|(?:tool(?:\s+call)?|arguments|parameters)\s*:.*` + // harness echoes
		`|exit code:?\s*\d+.*` +
		`|command (?:failed|not found|timed out).*` +
		`)\s*$`)

	blankRunRe = regexp.MustCompile(`\n{3,}`)
)

// fenceBody strips the opening/closing ``` markers (and language tag) so the
// body can be inspected structurally.
func fenceBody(fence string) string {
	body := strings.TrimSpace(fence)
	body = strings.TrimPrefix(body, "```")
	if i := strings.Index(body, "\n"); i >= 0 {
		first := strings.TrimSpace(body[:i])
		if first != "" && !strings.ContainsAny(first, " \t{}[]") {
			body = body[i+1:] // language tag line
		}
	}
	body = strings.TrimSuffix(strings.TrimSpace(body), "```")
	return strings.TrimSpace(body)
}

// isMachineOutputBody reports whether a fenced block body is pasted
// machine output rather than a code answer, using structural signals only:
// parseable data above snippet size, file frontmatter, indexed-row runs,
// tracebacks, exec markers, shell prompts, harness echoes.
func isMachineOutputBody(body string) bool {
	if body == "" {
		return false
	}
	// A fence containing nothing but one JSON value is pasted data, not a
	// code answer (answers carry explanation; data comes bare).
	if parsesJSON(body) {
		return true
	}
	if isDataDump(body) {
		return true
	}
	// File frontmatter: ---\nkey: value\nkey: value\n--- (any file type).
	if frontmatterRe.MatchString(body) {
		return true
	}
	lower := strings.ToLower(body)
	for _, s := range []string{
		"external_untrusted_content",
		"traceback (most recent call last)", "goroutine ",
		"[credentialed exec]", "stderr:",
	} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	indexed := 0
	for _, ln := range strings.Split(body, "\n") {
		if machineEchoLineRe.MatchString(ln) || stackTraceLineRe.MatchString(ln) {
			return true
		}
		if indexedRowRe.MatchString(ln) {
			indexed++
			if indexed >= 2 {
				return true
			}
		}
	}
	return false
}

// isDataDump reports whether text is a machine data block: it parses as a
// JSON object/array (or reads as key: value records) and is larger than a
// small illustrative snippet.
func isDataDump(body string) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return false
	}
	lines := strings.Count(trimmed, "\n") + 1
	big := lines >= minDumpLines || len(trimmed) >= minDumpChars
	if !big {
		return false
	}
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && parsesJSON(trimmed) {
		return true
	}
	// Key: value record dumps (YAML-ish tool output without braces).
	kv := 0
	for _, ln := range strings.Split(trimmed, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || t == "---" || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "#") {
			continue
		}
		if kvLineRe.MatchString(t) {
			kv++
			if kv >= 4 {
				return true
			}
		}
	}
	return false
}

// kvLineRe matches one `key: value` record line of YAML-ish tool output.
var kvLineRe = regexp.MustCompile(`^[A-Za-z_][\w\- ]{0,40}:\s+\S.*$`)

// parsesJSON reports whether the whole text is one JSON value.
func parsesJSON(s string) bool {
	var v any
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&v); err != nil {
		return false
	}
	return dec.More() == false && strings.TrimSpace(s) != ""
}

// dataBlockAhead decides whether the balanced brace/bracket block starting at
// line i is machine output: a generic error payload, or a data dump by
// structure and size. Small illustrative snippets are kept.
func dataBlockAhead(lines []string, i int) bool {
	depth := 0
	var sb strings.Builder
	for j := i; j < len(lines) && j < i+60; j++ {
		sb.WriteString(lines[j])
		sb.WriteString("\n")
		depth += strings.Count(lines[j], "{") + strings.Count(lines[j], "[") - strings.Count(lines[j], "}") - strings.Count(lines[j], "]")
		if depth <= 0 && j > i {
			break
		}
	}
	span := sb.String()
	if genericErrorKeyRe.MatchString(span) {
		return true
	}
	return isDataDump(span)
}
