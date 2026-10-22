package channels

import (
	"log/slog"
	"regexp"
	"strings"
)

// SanitizeOutbound redacts raw technical leakage from user-facing channel
// messages (Telegram, Zalo, ...). Agent replies are composed by an LLM that
// sometimes pastes tool output verbatim: =IMAGE() formulas, credentialed-exec
// error dumps, stack traces. Those must never reach end users; the details
// stay in daemon logs and agent memory.
//
// The function is deliberately conservative: it only touches well-known
// machine-generated shapes and leaves human-written content (including long
// data tables) untouched. It is a backstop, not a substitute for good tool
// descriptions and agent instructions.
//
// Call it on every outbound text path before sending or chunking.
func SanitizeOutbound(text string) string {
	if text == "" {
		return ""
	}
	redactions := 0

	// 1. =IMAGE("url", ...) formulas become the bare URL. Chat clients render
	// a bare image URL as a preview card, which is what the user wants to
	// see instead of the raw formula text. Handles HTML-escaped quotes.
	if imageFormulaRe.MatchString(text) {
		text = imageFormulaRe.ReplaceAllString(text, "$1")
		redactions++
	}

	lines := strings.Split(text, "\n")
	hasExecDump := strings.Contains(text, credExecMarker)
	kept := lines[:0]
	depth := 0 // JSON error-block brace depth
	skipping := false
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)

		// 2a. Skip a { ... } block when it is a machine error object
		// (identified by a "reason" key within the next few lines).
		if hasExecDump && !skipping && jsonBlockStartRe.MatchString(ln) && jsonErrorAhead(lines, i) {
			skipping = true
			depth = 0
		}
		if skipping {
			depth += strings.Count(ln, "{") - strings.Count(ln, "}")
			if depth <= 0 {
				skipping = false
			}
			redactions++
			continue
		}

		// 2b. Single diagnostic lines from credentialed-exec dumps.
		if hasExecDump && execDiagnosticLineRe.MatchString(ln) {
			redactions++
			continue
		}

		// 3. Stack traces (JS / Go / Python).
		if stackTraceLineRe.MatchString(ln) {
			redactions++
			continue
		}

		_ = trimmed
		kept = append(kept, ln)
	}

	out := strings.Join(kept, "\n")
	out = blankRunRe.ReplaceAllString(out, "\n\n")
	out = strings.TrimSpace(out)
	if out == "" {
		// The whole message was technical leakage: say so in plain language.
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
	// =IMAGE("https://...", 4, 150, 150) — quotes may be HTML-escaped and the
	// URL may contain &amp; entities or bare & separators. Captures the URL.
	imageFormulaRe = regexp.MustCompile(`(?i)=IMAGE\(\s*(?:&quot;|"|')((?:[^"'\s<])+?)(?:&quot;|"|')(?:\s*,[^)]*)?\)`)

	// A lone "{" line that opens a JSON error object dumped by a tool runner.
	jsonBlockStartRe = regexp.MustCompile(`^\s*\{\s*$`)

	// Single diagnostic lines, only stripped inside a credentialed-exec dump.
	execDiagnosticLineRe = regexp.MustCompile(`(?i)^\s*(?:\[CREDENTIALED EXEC\]|(?:Binary|Args|Note):|error\[(?:validation|auth|exec|timeout|credential)\]:|Usage:|For more information, try|STDERR:).*$`)

	// JS ("at fn (f.js:1:2)"), Go ("goroutine 1", "x.go:12"), Python tracebacks.
	stackTraceLineRe = regexp.MustCompile(`^\s*(?:at\s+\S.*\(.*:\d+.*\)|goroutine\s+\d+.*|.*\.go:\d+.*|Traceback \(most recent call last\):|File ".+", line \d+.*)$`)

	blankRunRe = regexp.MustCompile(`\n{3,}`)
)

// jsonErrorAhead reports whether the lines following index i (a lone "{")
// look like a machine error object — i.e. a "reason" key appears before the
// block plausibly ends. Bounds the scan so user data is never eaten.
func jsonErrorAhead(lines []string, i int) bool {
	depth := 0
	for j := i; j < len(lines) && j < i+20; j++ {
		lower := strings.ToLower(lines[j])
		if strings.Contains(lower, `"reason"`) {
			return true
		}
		depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
		if depth <= 0 && j > i {
			return false
		}
	}
	return false
}
