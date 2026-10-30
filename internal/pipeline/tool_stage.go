package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/hooks"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tracing"
)

const (
	maxSequentialWaitBatchMs     = 300000
	defaultParallelToolCallLimit = 4
)

// ToolStage runs per iteration after PruneStage. Executes tool calls from
// ThinkState.LastResponse, checks exit conditions (loop kill, read-only streak, budget).
type ToolStage struct {
	deps   *PipelineDeps
	result StageResult
}

// NewToolStage creates a ToolStage.
func NewToolStage(deps *PipelineDeps) *ToolStage {
	return &ToolStage{deps: deps, result: Continue}
}


// isWriteTool reports whether a tool mutates external state (sheet writes,
// formatting, file writes, cron, messages). Used by the fabrication guard in
// ObserveStage: a final response that CLAIMS such work must have at least one
// of these tools actually executed in the run.
func isWriteTool(name string) bool {
	writeTools := map[string]bool{
		// Google Sheets (MCP bridge names include the mcp_<server>__ prefix)
		"mcp_sheets__update_cells":              true,
		"mcp_sheets__batch_update_cells":        true,
		"mcp_sheets__batch_update":              true,
		"mcp_sheets__format_sheet_range":        true,
		"mcp_sheets__set_number_format":         true,
		"mcp_sheets__conditional_format":        true,
		"mcp_sheets__data_validation_dropdown":  true,
		"mcp_sheets__set_range_borders":         true,
		"mcp_sheets__resize_sheet_dimensions":   true,
		"mcp_sheets__auto_resize_columns":       true,
		"mcp_sheets__freeze_panes":              true,
		"mcp_sheets__merge_cells":               true,
		"mcp_sheets__rename_sheet_tab":          true,
		"mcp_sheets__delete_sheet_tab":          true,
		"mcp_sheets__insert_remove_rows":        true,
		"mcp_sheets__append_rows":               true,
		"mcp_sheets__clear_range":               true,
		"mcp_sheets__find_replace":              true,
		"mcp_sheets__add_filter":                true,
		"mcp_sheets__protect_sheet":             true,
		"mcp_sheets__upload_image_to_sheet":     true,
		"mcp_sheets__set_hyperlink":             true,
		"mcp_sheets__add_note":                  true,
		// builtin mutators
		"write_file": true, "edit": true, "exec": true, "bash": true,
		"cron": true, "send_file": true, "message": true,
		"create_image": true, "create_audio": true, "create_video": true,
	}
	if writeTools[name] {
		return true
	}
	// Fallback heuristic for MCP tools from other servers (docs, notebooklm,
	// leox...): their mutating tools never appear in the exact map above.
	// Strip the mcp_<server>__ prefix and match mutating verbs, excluding
	// known read-only prefixes so get_/list_/search_ stays read-only.
	if !strings.HasPrefix(name, "mcp_") {
		return false
	}
	base := name
	if idx := strings.LastIndex(name, "__"); idx >= 0 {
		base = name[idx+2:]
	}
	lower := strings.ToLower(base)
	for _, p := range []string{
		"get_", "list_", "read_", "search_", "query_", "describe",
		"status", "thumbnail", "select",
	} {
		if strings.HasPrefix(lower, p) || strings.Contains(lower, p) && (p == "status" || p == "thumbnail" || p == "select") {
			return false
		}
	}
	mutating := []string{
		"create", "update", "delete", "insert", "append", "write",
		"edit", "set_", "add_", "remove", "merge", "protect",
		"format", "upload", "clear", "rename", "resize", "freeze",
		"filter", "hyperlink", "validation", "border",
		"conditional", "save", "sync", "export", "import",
		"publish", "send", "post", "push",
	}
	for _, sub := range mutating {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

func (s *ToolStage) Name() string        { return "tool" }
func (s *ToolStage) Result() StageResult { return s.result }

// Execute extracts tool calls, dispatches them, checks exit conditions.
func (s *ToolStage) Execute(ctx context.Context, state *RunState) error {
	s.result = Continue

	resp := state.Think.LastResponse
	if resp == nil || len(resp.ToolCalls) == 0 {
		return nil // no tools — ThinkStage already set BreakLoop
	}

	toolCalls := resp.ToolCalls
	if s.deps.ExecuteToolCall == nil {
		return fmt.Errorf("ExecuteToolCall callback not configured")
	}

	// Surface this iteration's resolved tool allowlist to the tools themselves,
	// so a tool can introspect whether a sibling tool is available to the
	// calling agent (e.g. use_skill inlining skill content when read_file
	// isn't granted). Covers both the sequential and parallel dispatch paths
	// below since both derive their ctx from this one.
	if state.Tool.AllowedTools != nil {
		ctx = store.WithAvailableToolNames(ctx, state.Tool.AllowedTools)
	}

	// Parallel path: separate I/O (parallel) from state mutation (sequential).
	// Requires both ExecuteToolRaw and ProcessToolResult callbacks.
	if len(toolCalls) > 1 && s.canExecuteParallel(toolCalls) && !s.batchExceedsBudget(state, toolCalls) {
		preflight := s.preflightToolCalls(ctx, state, toolCalls)
		return s.executeParallel(ctx, state, preflight)
	}

	// Sequential fallback: ExecuteToolCall handles both I/O and state mutation.
	// Non-tool messages (warnings, nudges) are deferred until all tool results
	// are emitted to maintain correct tool_result grouping for OpenAI-compatible
	// providers. See https://github.com/nextlevelbuilder/goclaw/issues/1177
	cumulativeWaitMs := 0
	var deferredNonTool []providers.Message
	for _, tc := range toolCalls {
		if s.shouldStopBeforeTool(ctx, state) {
			appendDeferredMessages(state, deferredNonTool)
			return nil
		}
		if s.deps.SequentialToolCall != nil && s.deps.SequentialToolCall(tc) {
			cumulativeWaitMs += toolCallTimeMs(tc)
			if cumulativeWaitMs > maxSequentialWaitBatchMs {
				appendDeferredMessages(state, deferredNonTool)
				s.result = AbortRun
				return nil
			}
		}

		tc, blocked := s.preflightToolCall(ctx, state, tc)
		if blocked != nil {
			state.Messages.AppendPending(*blocked)
			state.Tool.TotalToolCalls++
			continue
		}

		msgs, err := s.deps.ExecuteToolCall(ctx, state, tc)
		if err != nil {
			return fmt.Errorf("execute tool %s: %w", tc.Name, err)
		}
		appendToolBatchMessages(state, msgs, &deferredNonTool)
		state.Tool.TotalToolCalls++
		if isWriteTool(tc.Name) && toolCallSucceeded(msgs) {
			state.Tool.WriteToolsUsed = append(state.Tool.WriteToolsUsed, tc.Name)
		}

		// Hook: async PostToolUse — fire and forget with detached context.
		// Parent the hook span to the tool span so it nests under the tool call.
		if s.deps.Hooks != nil {
			detached := context.WithoutCancel(ctx)
			if state.CurrentToolSpanID != nil {
				detached = tracing.WithParentSpanID(detached, *state.CurrentToolSpanID)
			}
			go s.deps.FireHook(detached, hooks.Event{ //nolint:errcheck
				EventID:   uuid.NewString(),
				SessionID: state.Input.SessionKey,
				TenantID:  store.TenantIDFromContext(ctx),
				AgentID:   store.AgentIDFromContext(ctx),
				SenderID:  state.Input.SenderID,
				UserID:    state.Input.UserID,
				ToolName:  tc.Name,
				ToolInput: tc.Arguments,
				HookEvent: hooks.EventPostToolUse,
			})
		}

		if state.Tool.LoopKilled {
			appendDeferredMessages(state, deferredNonTool)
			s.result = BreakLoop
			return nil
		}
		if ctx.Err() != nil {
			appendDeferredMessages(state, deferredNonTool)
			s.result = AbortRun
			return nil
		}
	}

	appendDeferredMessages(state, deferredNonTool)
	s.checkExitConditions(state)
	return nil
}

func (s *ToolStage) requiresSequential(toolCalls []providers.ToolCall) bool {
	if s.deps.SequentialToolCall == nil {
		return false
	}
	return slices.ContainsFunc(toolCalls, s.deps.SequentialToolCall)
}

type toolCallPreflightItem struct {
	index   int
	tc      providers.ToolCall
	blocked *providers.Message
}

type toolCallPreflight struct {
	ordered    []toolCallPreflightItem
	executable []toolCallPreflightItem
}

func (s *ToolStage) preflightToolCalls(ctx context.Context, state *RunState, toolCalls []providers.ToolCall) toolCallPreflight {
	out := toolCallPreflight{
		ordered: make([]toolCallPreflightItem, 0, len(toolCalls)),
	}
	for i, tc := range toolCalls {
		tc, blocked := s.preflightToolCall(ctx, state, tc)
		item := toolCallPreflightItem{index: i, tc: tc, blocked: blocked}
		out.ordered = append(out.ordered, item)
		if blocked == nil {
			out.executable = append(out.executable, item)
		}
	}
	return out
}

func (s *ToolStage) preflightToolCall(ctx context.Context, state *RunState, tc providers.ToolCall) (providers.ToolCall, *providers.Message) {
	if s.deps.AuthorizeToolCall != nil {
		if ok, reason := s.deps.AuthorizeToolCall(ctx, state, tc); !ok {
			return tc, &providers.Message{
				Role:       "tool",
				Content:    reason,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				IsError:    true,
			}
		}
	}
	// Parent the pre_tool_use hook span to the current LLM-call span so it nests
	// alongside the tool call it gates.
	hookCtx := ctx
	if state.CurrentLLMSpanID != nil {
		hookCtx = tracing.WithParentSpanID(ctx, *state.CurrentLLMSpanID)
	}
	r, _ := s.deps.FireHook(hookCtx, hooks.Event{
		EventID:   uuid.NewString(),
		SessionID: state.Input.SessionKey,
		TenantID:  store.TenantIDFromContext(ctx),
		AgentID:   store.AgentIDFromContext(ctx),
		SenderID:  state.Input.SenderID,
		UserID:    state.Input.UserID,
		ToolName:  tc.Name,
		ToolInput: tc.Arguments,
		HookEvent: hooks.EventPreToolUse,
	})
	if r.Decision == hooks.DecisionBlock {
		return tc, &providers.Message{
			Role:       "tool",
			Content:    "Hook blocked: pre_tool_use",
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
		}
	}
	if r.UpdatedToolInput != nil {
		tc.Arguments = r.UpdatedToolInput
	}
	return tc, nil
}

func (s *ToolStage) canExecuteParallel(toolCalls []providers.ToolCall) bool {
	if s.deps.ExecuteToolRaw == nil || s.deps.ProcessToolResult == nil || s.deps.ParallelEligibleToolCall == nil {
		return false
	}
	if s.requiresSequential(toolCalls) {
		return false
	}
	for _, tc := range toolCalls {
		if !s.deps.ParallelEligibleToolCall(tc) {
			return false
		}
	}
	return true
}

func (s *ToolStage) batchExceedsBudget(state *RunState, toolCalls []providers.ToolCall) bool {
	return s.deps.Config.MaxToolCalls > 0 && state.Tool.TotalToolCalls+len(toolCalls) > s.deps.Config.MaxToolCalls
}

func (s *ToolStage) shouldStopBeforeTool(ctx context.Context, state *RunState) bool {
	if ctx.Err() != nil {
		s.result = AbortRun
		return true
	}
	if s.deps.Config.MaxToolCalls > 0 && state.Tool.TotalToolCalls >= s.deps.Config.MaxToolCalls {
		s.result = BreakLoop
		return true
	}
	return false
}

func toolCallTimeMs(tc providers.ToolCall) int {
	v, ok := tc.Arguments["timeMs"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := strconv.Atoi(n.String())
		if err == nil {
			return i
		}
	}
	return 0
}

// executeParallel runs tool I/O concurrently, then processes results sequentially.
func (s *ToolStage) executeParallel(ctx context.Context, state *RunState, preflight toolCallPreflight) error {
	type rawResult struct {
		index   int
		tc      providers.ToolCall
		msg     providers.Message
		rawData any
		err     error
	}

	startedAt := time.Now()
	slog.Info("tool.parallel.batch.start",
		"session", state.Input.SessionKey,
		"run_id", state.RunID,
		"count", len(preflight.executable),
		"limit", defaultParallelToolCallLimit)

	// Phase 1: parallel I/O (no state mutation)
	results := make([]rawResult, len(preflight.executable))
	sem := make(chan struct{}, defaultParallelToolCallLimit)
	var wg sync.WaitGroup
	for i, item := range preflight.executable {
		wg.Add(1)
		go func(idx int, item toolCallPreflightItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[idx] = rawResult{index: item.index, tc: item.tc, err: ctx.Err()}
				return
			}
			itemCtx := ctx
			if state.CurrentLLMSpanID != nil {
				itemCtx = tracing.WithParentSpanID(ctx, *state.CurrentLLMSpanID)
			}
			msg, rawData, err := s.deps.ExecuteToolRaw(itemCtx, item.tc)
			results[idx] = rawResult{index: item.index, tc: item.tc, msg: msg, rawData: rawData, err: err}
		}(i, item)
	}
	wg.Wait()
	slog.Info("tool.parallel.batch.end",
		"session", state.Input.SessionKey,
		"run_id", state.RunID,
		"count", len(preflight.executable),
		"limit", defaultParallelToolCallLimit,
		"duration_ms", time.Since(startedAt).Milliseconds())

	// Phase 2: sequential state mutation (safe, deterministic order).
	// Non-tool messages deferred until all tool results are emitted (#1177).
	resultByIndex := make(map[int]rawResult, len(results))
	for _, r := range results {
		resultByIndex[r.index] = r
	}
	var deferredNonTool []providers.Message
	for _, item := range preflight.ordered {
		tc := item.tc
		if item.blocked != nil {
			state.Messages.AppendPending(*item.blocked)
			state.Tool.TotalToolCalls++
			continue
		}
		r, ok := resultByIndex[item.index]
		if !ok {
			return fmt.Errorf("execute tool %s: missing parallel result", tc.Name)
		}
		if r.err != nil {
			return fmt.Errorf("execute tool %s: %w", tc.Name, r.err)
		}
		processed := s.deps.ProcessToolResult(ctx, state, tc, r.msg, r.rawData)
		appendToolBatchMessages(state, processed, &deferredNonTool)
		state.Tool.TotalToolCalls++
		if isWriteTool(tc.Name) && toolCallSucceeded(processed) {
			state.Tool.WriteToolsUsed = append(state.Tool.WriteToolsUsed, tc.Name)
		}

		// Hook: async PostToolUse for parallel path — fire and forget.
		// PreToolUse already ran in preflight before any raw I/O was scheduled.
		// The parallel tool I/O is parented to the LLM span (see ExecuteToolRaw
		// call site above), so parent the parallel hook span there too — the
		// per-tool span ID is not plumbed back to the pipeline from the opaque
		// rawData payload, so the LLM span is the correct shared ancestor.
		if s.deps.Hooks != nil {
			detached := context.WithoutCancel(ctx)
			if state.CurrentLLMSpanID != nil {
				detached = tracing.WithParentSpanID(detached, *state.CurrentLLMSpanID)
			}
			go s.deps.FireHook(detached, hooks.Event{ //nolint:errcheck
				EventID:   uuid.NewString(),
				SessionID: state.Input.SessionKey,
				TenantID:  store.TenantIDFromContext(ctx),
				AgentID:   store.AgentIDFromContext(ctx),
				SenderID:  state.Input.SenderID,
				UserID:    state.Input.UserID,
				ToolName:  tc.Name,
				ToolInput: tc.Arguments,
				HookEvent: hooks.EventPostToolUse,
			})
		}

		if state.Tool.LoopKilled {
			appendDeferredMessages(state, deferredNonTool)
			s.result = BreakLoop
			return nil
		}
	}

	appendDeferredMessages(state, deferredNonTool)
	s.checkExitConditions(state)
	return nil
}

// toolCallSucceeded reports whether a tool execution batch contains a
// successful tool result. A batch with no tool-role message (should not
// happen outside tests) is treated as failure so a failed write is never
// recorded as evidence for the fabrication guard in ObserveStage.
func toolCallSucceeded(msgs []providers.Message) bool {
	for _, msg := range msgs {
		if msg.Role == "tool" {
			return !msg.IsError
		}
	}
	return false
}

// appendToolBatchMessages appends tool-role messages immediately and defers
// non-tool messages (warnings, nudges) so that all tool results for a single
// assistant turn stay contiguous. This prevents OpenAI-compatible providers
// from rejecting the transcript due to interleaved user messages (#1177).
func appendToolBatchMessages(state *RunState, msgs []providers.Message, deferred *[]providers.Message) {
	for _, msg := range msgs {
		if msg.Role == "tool" {
			state.Messages.AppendPending(msg)
		} else {
			*deferred = append(*deferred, msg)
		}
	}
}

// appendDeferredMessages flushes deferred non-tool messages into pending.
func appendDeferredMessages(state *RunState, deferred []providers.Message) {
	for _, msg := range deferred {
		state.Messages.AppendPending(msg)
	}
}

// checkExitConditions checks read-only streak and tool budget.
func (s *ToolStage) checkExitConditions(state *RunState) {
	if state.Tool.LoopKilled {
		s.result = BreakLoop
		return
	}
	if s.deps.CheckReadOnly != nil {
		warningMsg, shouldBreak := s.deps.CheckReadOnly(state)
		if warningMsg != nil {
			state.Messages.AppendPending(*warningMsg)
		}
		if shouldBreak {
			s.result = BreakLoop
			return
		}
	}
	if s.deps.Config.MaxToolCalls > 0 && state.Tool.TotalToolCalls >= s.deps.Config.MaxToolCalls {
		s.result = BreakLoop
	}
}
