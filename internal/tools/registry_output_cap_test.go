package tools

import (
	"context"
	"strings"
	"testing"
)

// stubTool returns a fixed ForLLM payload for post-execute validation tests.
type stubTool struct {
	name    string
	payload string
}

func (s *stubTool) Name() string               { return s.name }
func (s *stubTool) Description() string        { return "stub" }
func (s *stubTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (s *stubTool) Execute(_ context.Context, _ map[string]any) *Result {
	return NewResult(s.payload)
}

// TestRegistry_CapsGiantOutput proves post-execute validation: one huge tool
// dump cannot blow the run context — it is truncated and marked.
func TestRegistry_CapsGiantOutput(t *testing.T) {
	r := NewRegistry()
	big := strings.Repeat("row-data-0123456789 ", 4000) // ~80k chars
	r.Register(&stubTool{name: "bigreader", payload: big})

	res := r.Execute(context.Background(), "bigreader", map[string]any{})
	if res.IsError {
		t.Fatalf("cap must not turn success into error: %s", res.ForLLM)
	}
	if len([]rune(res.ForLLM)) > maxToolOutputChars+2000 {
		t.Fatalf("output not bounded, len=%d", len([]rune(res.ForLLM)))
	}
	if !strings.Contains(res.ForLLM, "truncated") {
		t.Errorf("truncation must be marked:\n%.200s...", res.ForLLM)
	}
	if !strings.HasPrefix(res.ForLLM, "row-data") {
		t.Errorf("truncation must keep the head:\n%.120s...", res.ForLLM)
	}
}

// TestRegistry_SmallOutputUntouched ensures normal outputs pass through
// byte-identical (no marker tax on every call).
func TestRegistry_SmallOutputUntouched(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "small", payload: "hello"})
	res := r.Execute(context.Background(), "small", map[string]any{})
	if res.ForLLM != "hello" {
		t.Fatalf("small output modified: %q", res.ForLLM)
	}
}
