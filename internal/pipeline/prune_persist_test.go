package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// TestPruneStage_Compaction_PersistsSynchronously proves fix 5: a mid-loop
// compaction is written back through PersistCompactedHistory inline, so a
// crash/restart cannot silently discard it.
func TestPruneStage_Compaction_PersistsSynchronously(t *testing.T) {
	t.Parallel()
	var persisted [][]providers.Message
	deps := PipelineDeps{
		Config:       PipelineConfig{ContextWindow: 1000, MaxTokens: 100},
		TokenCounter: &mockTokenCounter{countPerMessage: 100},
		PruneMessages: func(msgs []providers.Message, _ int) ([]providers.Message, PruneStats) {
			return msgs, PruneStats{} // no reduction — force compaction path
		},
		CompactMessages: func(_ context.Context, _ []providers.Message, _ string) ([]providers.Message, error) {
			return []providers.Message{{Role: "user", Content: "[compacted summary]"}}, nil
		},
		PersistCompactedHistory: func(_ context.Context, _ string, compacted []providers.Message) error {
			persisted = append(persisted, compacted)
			return nil
		},
	}
	stage := NewPruneStage(&deps, nil)
	state := defaultState()
	history := make([]providers.Message, 50) // 5000 tokens >> budget 900
	for i := range history {
		history[i] = providers.Message{Role: "user", Content: "msg"}
	}
	state.Messages.SetHistory(history)

	if err := stage.Execute(context.Background(), state); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !state.Prune.MidLoopCompacted {
		t.Fatal("precondition: MidLoopCompacted must be true")
	}
	if len(persisted) != 1 {
		t.Fatalf("PersistCompactedHistory called %d times, want 1", len(persisted))
	}
	if len(persisted[0]) != 1 || persisted[0][0].Content != "[compacted summary]" {
		t.Fatalf("persisted wrong history: %#v", persisted)
	}
}

// TestPruneStage_CompactionPersistError_DoesNotKillRun ensures a store hiccup
// during inline persist degrades to a warning, not a failed run.
func TestPruneStage_CompactionPersistError_DoesNotKillRun(t *testing.T) {
	t.Parallel()
	deps := PipelineDeps{
		Config:       PipelineConfig{ContextWindow: 1000, MaxTokens: 100},
		TokenCounter: &mockTokenCounter{countPerMessage: 100},
		PruneMessages: func(msgs []providers.Message, _ int) ([]providers.Message, PruneStats) {
			return msgs, PruneStats{}
		},
		CompactMessages: func(_ context.Context, _ []providers.Message, _ string) ([]providers.Message, error) {
			return []providers.Message{{Role: "user", Content: "[compacted summary]"}}, nil
		},
		PersistCompactedHistory: func(_ context.Context, _ string, _ []providers.Message) error {
			return errors.New("db unavailable")
		},
	}
	stage := NewPruneStage(&deps, nil)
	state := defaultState()
	history := make([]providers.Message, 50)
	for i := range history {
		history[i] = providers.Message{Role: "user", Content: "msg"}
	}
	state.Messages.SetHistory(history)

	if err := stage.Execute(context.Background(), state); err != nil {
		t.Fatalf("persist failure must not fail the run, got: %v", err)
	}
	if !state.Prune.MidLoopCompacted {
		t.Fatal("in-memory compaction must still apply")
	}
}
