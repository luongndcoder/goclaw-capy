package pipeline

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// TestToolStage_WriteToolsUsed_OnlyOnSuccess is the regression guard for the
// premature-completion bug: a failed write tool call must NOT count as
// evidence in WriteToolsUsed, otherwise the fabrication guard in ObserveStage
// is bypassed and the model can claim "done" after an error.
func TestToolStage_WriteToolsUsed_OnlyOnSuccess(t *testing.T) {
	t.Parallel()

	newDeps := func(isError bool) *PipelineDeps {
		return &PipelineDeps{
			Config: PipelineConfig{MaxToolCalls: 100},
			ExecuteToolCall: func(_ context.Context, _ *RunState, tc providers.ToolCall) ([]providers.Message, error) {
				return []providers.Message{{
					Role:       "tool",
					Content:    "result",
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					IsError:    isError,
				}}, nil
			},
		}
	}

	// Success case: write tool recorded.
	stage := NewToolStage(newDeps(false))
	st := defaultState()
	setLastResponse(st, []providers.ToolCall{toolCallFor("write_file")})
	if err := stage.Execute(context.Background(), st); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(st.Tool.WriteToolsUsed) != 1 {
		t.Fatalf("successful write must be recorded, got %v", st.Tool.WriteToolsUsed)
	}

	// Failure case: write tool NOT recorded.
	stage = NewToolStage(newDeps(true))
	st = defaultState()
	setLastResponse(st, []providers.ToolCall{toolCallFor("write_file")})
	if err := stage.Execute(context.Background(), st); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(st.Tool.WriteToolsUsed) != 0 {
		t.Fatalf("failed write must NOT be recorded, got %v", st.Tool.WriteToolsUsed)
	}
}

// TestToolStage_WriteToolsUsed_ParallelOnlyOnSuccess covers the parallel
// dispatch path (2+ tools): same rule must hold after ProcessToolResult.
func TestToolStage_WriteToolsUsed_ParallelOnlyOnSuccess(t *testing.T) {
	t.Parallel()

	newParallelDeps := func(isError bool) *PipelineDeps {
		return &PipelineDeps{
			Config: PipelineConfig{MaxToolCalls: 100},
			ExecuteToolCall: func(_ context.Context, _ *RunState, tc providers.ToolCall) ([]providers.Message, error) {
				return []providers.Message{{Role: "tool", Content: "ok", ToolCallID: tc.ID}}, nil
			},
			ExecuteToolRaw: func(_ context.Context, tc providers.ToolCall) (providers.Message, any, error) {
				return providers.Message{
					Role:       "tool",
					Content:    "raw",
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					IsError:    isError,
				}, nil, nil
			},
			ProcessToolResult: func(_ context.Context, _ *RunState, tc providers.ToolCall, rawMsg providers.Message, _ any) []providers.Message {
				return []providers.Message{rawMsg}
			},
			ParallelEligibleToolCall: func(providers.ToolCall) bool { return true },
		}
	}

	stage := NewToolStage(newParallelDeps(true))
	st := defaultState()
	setLastResponse(st, []providers.ToolCall{toolCallFor("write_file"), toolCallFor("exec")})
	if err := stage.Execute(context.Background(), st); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(st.Tool.WriteToolsUsed) != 0 {
		t.Fatalf("failed parallel writes must NOT be recorded, got %v", st.Tool.WriteToolsUsed)
	}
}
