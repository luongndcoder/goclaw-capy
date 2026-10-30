package agent

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// TestProcessToolResult_AsyncOutstandingCounting proves the fix-5 counter: an
// async spawn raises the outstanding obligation, a successful spawn get
// clears one, and failed gets or sync work leave it untouched.
func TestProcessToolResult_AsyncOutstandingCounting(t *testing.T) {
	loop := &Loop{id: "test-agent"}
	rs := &runState{}
	emit := func(AgentEvent) {}
	req := &RunRequest{RunID: "run-1"}
	ctx := context.Background()

	spawnAsync := func() {
		loop.processToolResult(ctx, rs, req, emit,
			providers.ToolCall{ID: "s1", Name: "spawn"},
			"spawn",
			&tools.Result{ForLLM: `{"status":"accepted"}`, Async: true},
			false)
	}

	spawnAsync()
	spawnAsync()
	if rs.asyncOutstanding != 2 {
		t.Fatalf("outstanding = %d, want 2 after two async spawns", rs.asyncOutstanding)
	}

	// Failed get clears nothing.
	loop.processToolResult(ctx, rs, req, emit,
		providers.ToolCall{ID: "g0", Name: "spawn", Arguments: map[string]any{"action": "get"}},
		"spawn",
		tools.ErrorResult("completion not found"),
		false)
	if rs.asyncOutstanding != 2 {
		t.Fatalf("outstanding = %d, want 2 after failed get", rs.asyncOutstanding)
	}

	// Successful get clears one.
	loop.processToolResult(ctx, rs, req, emit,
		providers.ToolCall{ID: "g1", Name: "spawn", Arguments: map[string]any{"action": "get"}},
		"spawn",
		tools.NewResult(`{"status":"completed"}`),
		false)
	if rs.asyncOutstanding != 1 {
		t.Fatalf("outstanding = %d, want 1 after one successful get", rs.asyncOutstanding)
	}

	// Non-spawn async work (e.g. delegate) must not touch the spawn counter.
	loop.processToolResult(ctx, rs, req, emit,
		providers.ToolCall{ID: "d1", Name: "delegate"},
		"delegate",
		&tools.Result{ForLLM: "delegated", Async: true},
		false)
	if rs.asyncOutstanding != 1 {
		t.Fatalf("outstanding = %d, want 1 (non-spawn async ignored)", rs.asyncOutstanding)
	}

	// Counter never goes negative.
	loop.processToolResult(ctx, rs, req, emit,
		providers.ToolCall{ID: "g2", Name: "spawn", Arguments: map[string]any{"action": "get"}},
		"spawn",
		tools.NewResult(`{"status":"completed"}`),
		false)
	loop.processToolResult(ctx, rs, req, emit,
		providers.ToolCall{ID: "g3", Name: "spawn", Arguments: map[string]any{"action": "get"}},
		"spawn",
		tools.NewResult(`{"status":"completed"}`),
		false)
	if rs.asyncOutstanding != 0 {
		t.Fatalf("outstanding = %d, want 0 (floor at zero)", rs.asyncOutstanding)
	}
}
