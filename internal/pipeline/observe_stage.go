package pipeline

import (
	"strconv"
	"strings"
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/hooks"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// ObserveStage runs per iteration after ToolStage. Drains InjectCh,
// accumulates final content when no tool calls, tracks block replies.
// Does NOT implement StageWithResult — never controls flow.
type ObserveStage struct {
	deps *PipelineDeps
}

// NewObserveStage creates an ObserveStage.
func NewObserveStage(deps *PipelineDeps) *ObserveStage {
	return &ObserveStage{deps: deps}
}

func (s *ObserveStage) Name() string { return "observe" }

// Execute drains injected messages, accumulates final content + block replies.
func (s *ObserveStage) Execute(_ context.Context, state *RunState) error {
	injected := s.drainInjectedMessages()

	resp := state.Think.LastResponse
	if resp == nil {
		appendPendingMessages(state, injected)
		return nil
	}

	// Track block replies only for tool-iteration responses. Final answers do
	// not count, otherwise gateway dedup can suppress delivery.
	if resp.Content != "" && len(resp.ToolCalls) > 0 {
		state.Observe.BlockReplies++
		state.Observe.LastBlockReply = resp.Content
	}

	if len(resp.ToolCalls) == 0 {
		s.observeFinalResponse(state, resp, injected)
	} else {
		appendPendingMessages(state, injected)
	}

	s.accumulateAssistantImages(state, resp)
	return nil
}

// maxGuardBlocks caps block-and-retry per run for the async and fabrication
// gates. Past the cap the claim is delivered with unverifiedNotice, never as
// a false confirmation.
const maxGuardBlocks = 3

// unverifiedNotice prefixes a completion claim that survived all guard
// retries without producing tool evidence. The user sees honesty ("not yet
// verified") instead of a fabricated "done".
const unverifiedNotice = "⚠️ Chưa xác minh: hệ thống chưa thấy bằng chứng đã làm xong việc dưới đây.\n\n"

// guardToneGuidance coaches the model to maintain conversational tact during retry turns
// so the user receives a warm, helpful response instead of an abrupt error report.
const guardToneGuidance = " Lưu ý quan trọng khi trả lời lại người dùng: hãy giữ nguyên phong cách ấm áp, tự nhiên và cách xưng hô khéo léo trong SOUL.md; diễn đạt nhẹ nhàng, chu đáo (như đang kiểm tra hoặc cập nhật giúp họ), tuyệt đối KHÔNG dùng giọng văn cứng nhắc kiểu thông báo lỗi hệ thống."

// blockFinal records a blocked final response and injects the retry
// instruction so the model gets another turn to ground its claim.
func (s *ObserveStage) blockFinal(state *RunState, content, thinking, reason string) {
	state.Messages.AppendPending(providers.Message{
		Role:     "assistant",
		Content:  content,
		Thinking: thinking,
	})
	state.Messages.AppendPending(providers.Message{Role: "user", Content: reason})
	state.Observe.BlockedByHook = true
	state.Observe.HookRejectionReason = reason
	state.Observe.ContinueAfterFinal = true
}

func (s *ObserveStage) drainInjectedMessages() []providers.Message {
	if s.deps.DrainInjectCh == nil {
		return nil
	}
	return s.deps.DrainInjectCh()
}

func (s *ObserveStage) observeFinalResponse(state *RunState, resp *providers.ChatResponse, injected []providers.Message) {
	content, thinking := splitTaggedThinkingContent(resp.Content, resp.Thinking)

	// ── Async gate ─────────────────────────────────────────────────
	// The run spawned background work (spawn async) whose durable result has
	// not been retrieved via spawn(action="get") yet, but the model claims
	// completion. The subagent result is not in context (no injected
	// announcement arrived), so the claim cannot be grounded — block it and
	// point at the retrieval tool. Honest progress reports ("đã giao việc,
	// có kết quả sẽ báo") carry no completion claim and pass through.
	// Past the block cap the claim is delivered with an "unverified" label
	// instead of a false confirmation (never surrender silently).
	if len(injected) == 0 &&
		claimsWriteCompletion(content) && state.Tool.AsyncOutstanding > 0 {
		if state.Observe.FabricationBlocks < maxGuardBlocks {
			state.Observe.FabricationBlocks++
			reason := "CHƯA XONG: bạn đã giao việc cho subagent chạy nền nhưng chưa lấy kết quả " +
				"(còn " + strconv.Itoa(state.Tool.AsyncOutstanding) + " việc chưa thu hồi). " +
				"Hãy gọi spawn(action=\"get\", completion_id=\"...\") để lấy kết quả thật, " +
				"đối chiếu rồi mới báo hoàn thành. Nếu việc nền chưa xong, hãy nói thật với " +
				"người dùng là đang chờ thay vì báo đã xong." +
				guardToneGuidance
			s.blockFinal(state, content, thinking, reason)
			slog.Warn("async_guard.blocked",
				"session", state.Input.SessionKey,
				"agent", store.AgentIDFromContext(state.Ctx),
				"outstanding", state.Tool.AsyncOutstanding,
				"content_preview", truncateForLog(content, 120))
			return
		}
		content = unverifiedNotice + content
		slog.Warn("async_guard.surrendered_unverified",
			"session", state.Input.SessionKey,
			"agent", store.AgentIDFromContext(state.Ctx),
			"outstanding", state.Tool.AsyncOutstanding,
			"content_preview", truncateForLog(content, 160))
	}

	// ── Fabrication guard ────────────────────────────────────────────
	// The model sometimes CLAIMS it wrote/formatted data while the run
	// executed no write tool at all (verified incident: only get_sheet_data
	// was called, yet the reply said "đã áp dụng định dạng,
	// đã đặt QR..."). Block such a reply and retry truthfully.
	// Exemption: a run with zero tool calls is pure Q&A — generic closings
	// ("hoàn thành", "xong rồi") there are politeness, not write claims.
	// Past the block cap the claim is delivered with an "unverified" label
	// instead of a false confirmation (never surrender silently).
	if len(injected) == 0 &&
		claimsWriteCompletion(content) && len(state.Tool.WriteToolsUsed) == 0 &&
		state.Tool.TotalToolCalls > 0 {
		if state.Observe.FabricationBlocks < maxGuardBlocks {
			state.Observe.FabricationBlocks++
			reason := "PHÁT HIỆN BỊA ĐẶT: bạn vừa nói đã cập nhật/định dạng/thêm/sửa dữ liệu, " +
				"NHƯNG trong lượt này bạn KHÔNG hề gọi bất kỳ công cụ ghi nào (update_cells, " +
				"set_number_format, format_sheet_range, append_rows, write_file...). Bạn chỉ " +
				"đọc dữ liệu. Hãy THỰC SỰ gọi công cụ ghi để làm việc đó, sau đó đọc lại để " +
				"xác nhận, rồi mới báo hoàn thành. Nếu chưa làm được, hãy nói thật với người " +
				"dùng là chưa thực hiện." +
				guardToneGuidance
			s.blockFinal(state, content, thinking, reason)
			slog.Warn("fabrication_guard.blocked",
				"session", state.Input.SessionKey,
				"agent", store.AgentIDFromContext(state.Ctx),
				"write_tools_used", len(state.Tool.WriteToolsUsed),
				"content_preview", truncateForLog(content, 120))
			return
		}
		content = unverifiedNotice + content
		slog.Warn("fabrication_guard.surrendered_unverified",
			"session", state.Input.SessionKey,
			"agent", store.AgentIDFromContext(state.Ctx),
			"write_tools_used", len(state.Tool.WriteToolsUsed),
			"content_preview", truncateForLog(content, 160))
	}

	// Fire post_model_response hook for final responses (no tool calls).
	// This is a blocking hook that can prevent user delivery and inject a retry.
	if s.deps.Hooks != nil && len(injected) == 0 {
		ev := hooks.Event{
			EventID:       uuid.NewString(),
			SessionID:     state.Input.SessionKey,
			TenantID:      store.TenantIDFromContext(state.Ctx),
			AgentID:       store.AgentIDFromContext(state.Ctx),
			SenderID:      state.Input.SenderID,
			UserID:        state.Input.UserID,
			HookEvent:     hooks.EventPostModelResponse,
			ModelResponse: content,
			Thinking:      thinking,
			ToolCalls:     resp.ToolCalls,
		}
		result, err := s.deps.FireHook(state.Ctx, ev)
		if err != nil {
			slog.Debug("post_model_response hook error", "error", err)
		}
		if result.Decision == hooks.DecisionBlock {
			// Hook blocked delivery: keep assistant message in history, inject
			// rejection reason as user message, and continue to next iteration.
			state.Messages.AppendPending(providers.Message{
				Role:     "assistant",
				Content:  content,
				Thinking: thinking,
			})
			rejectionMsg := result.DecisionReason
			if rejectionMsg == "" {
				rejectionMsg = "Response blocked by policy."
			}
			state.Messages.AppendPending(providers.Message{
				Role:    "user",
				Content: rejectionMsg,
			})
			state.Observe.BlockedByHook = true
			state.Observe.HookRejectionReason = rejectionMsg
			state.Observe.ContinueAfterFinal = true
			return
		}
	}

	if len(injected) == 0 {
		state.Observe.FinalContent = content
		state.Observe.FinalThinking = thinking
		return
	}

	state.Messages.AppendPending(providers.Message{
		Role:      "assistant",
		Content:   content,
		Thinking:  thinking,
		Transient: true,
	})
	appendPendingMessages(state, injected)
	state.Observe.FinalContent = ""
	state.Observe.FinalThinking = ""
	state.Observe.ContinueAfterFinal = true
}

func (s *ObserveStage) accumulateAssistantImages(state *RunState, resp *providers.ChatResponse) {
	if len(resp.Images) == 0 {
		return
	}
	for _, img := range resp.Images {
		if img.Partial {
			continue
		}
		state.Observe.AssistantImages = append(state.Observe.AssistantImages, img)
	}
	// Clear on response so a re-processing pass (for example a retry) does not double-count.
	resp.Images = nil
}

func appendPendingMessages(state *RunState, messages []providers.Message) {
	for _, msg := range messages {
		state.Messages.AppendPending(msg)
	}
}

// claimsWriteCompletion reports whether the assistant text asserts that it
// WROTE/MODIFIED data (as opposed to merely reading or answering). Used by the
// fabrication guard in ObserveStage.
func claimsWriteCompletion(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, c := range writeClaims {
		start := 0
		for {
			idx := strings.Index(lower[start:], c)
			if idx < 0 {
				break
			}
			absIdx := start + idx
			if !isNegatedOccurrence(lower, absIdx) {
				return true
			}
			start = absIdx + len(c)
		}
	}
	return false
}

var writeClaims = []string{
	// Vietnamese past/completed write actions
	"đã áp dụng", "đã đặt", "đã chuyển công thức", "đã cập nhật",
	"đã chỉnh", "đã định dạng", "đã format", "đã thêm",
	"đã xoá", "đã xóa", "đã ghi", "đã sửa", "đã tạo",
	"đã đổi", "đã set", "đã thiết lập", "đã nới rộng",
	"đã cài đặt", "đã hoàn thiện",
	"đã bổ sung", "đã điền", "đã gán", "đã chèn",
	"đã lưu", "đã nhập", "đã đồng bộ", "đã xuất",
	"đã xử lý xong", "đã làm xong",
	// Vietnamese write verbs with completion suffixes
	"cập nhật xong", "ghi xong", "sửa xong", "thêm xong",
	"định dạng xong", "format xong", "lưu xong", "xử lý xong",
	"nhập xong", "nhập liệu xong", "xóa xong", "xoá xong",
	"tạo xong", "điền xong", "gán xong", "chèn xong",
	"đồng bộ xong", "thiết lập xong", "cài đặt xong",
	// Explicit write completion compounds
	"hoàn thành việc nhập", "hoàn thành việc cập nhật", "hoàn thành việc sửa",
	"hoàn tất việc nhập", "hoàn tất việc cập nhật", "hoàn tất việc sửa",
	"hoàn tất đồng bộ", "hoàn thành đồng bộ", "hoàn thành nhập liệu",
	"hoàn tất nhập liệu", "hoàn thành cập nhật", "hoàn tất cập nhật",
	"hoàn tất chỉnh sửa", "hoàn thành chỉnh sửa",
	// English write claims
	"i have updated", "i updated", "i have formatted", "i formatted",
	"i have set", "i set the", "i have added", "i added", "i have saved",
	"i saved", "i have created", "i created", "done - updated",
	"has been updated", "have been updated", "been updated",
	"has been saved", "have been saved", "been saved",
	"has been created", "have been created", "been created",
	"successfully updated", "successfully saved", "written to",
	"i exported", "has been exported", "have been exported", "exported to",
	"i synced", "has been synced", "have been synced", "synced to", "synced with",
}

// isNegatedOccurrence checks whether an occurrence of a claim phrase at idx
// is preceded by negation or future/intent markers in the current clause.
func isNegatedOccurrence(text string, idx int) bool {
	if idx <= 0 {
		return false
	}
	prefix := text[:idx]

	// Find the start of the current clause.
	clauseStart := 0
	clauseSeparators := []string{
		".", ",", "!", "?", ";", ":", "\n", "(", ")", "[", "]", "—", "-",
		" nhưng ", " nhưng mà ", " còn ", " tuy nhiên ", " song ", " but ", " however ", " whereas ",
	}
	for _, sep := range clauseSeparators {
		if pos := strings.LastIndex(prefix, sep); pos >= 0 {
			posAfter := pos + len(sep)
			if posAfter > clauseStart {
				clauseStart = posAfter
			}
		}
	}

	clausePrefix := strings.TrimSpace(prefix[clauseStart:])
	if clausePrefix == "" {
		return false
	}

	tokens := strings.Fields(clausePrefix)
	n := len(tokens)
	if n == 0 {
		return false
	}

	// 1. Two-word negation/intent phrases (checked at end of clausePrefix or up to 3 tokens back).
	twoWordPhrases := map[string]bool{
		"chưa thể":   true,
		"chưa kịp":   true,
		"chưa hề":    true,
		"không thể":  true,
		"chẳng thể":  true,
		"để em":      true,
		"để tôi":     true,
		"để mình":    true,
		"unable to":  true,
		"going to":   true,
		"need to":    true,
	}
	for i := max(0, n-3); i < n-1; i++ {
		pair := tokens[i] + " " + tokens[i+1]
		if twoWordPhrases[pair] {
			return true
		}
	}

	// 2. Single-word negation/intent words.
	singleWordNegations := map[string]bool{
		// Vietnamese
		"chưa": true, "không": true, "chẳng": true, "chả": true,
		"sắp": true, "sẽ": true, "cần": true, "muốn": true, "định": true, "nên": true,
		// English
		"not": true, "never": true, "haven't": true, "hasn't": true,
		"didn't": true, "cannot": true, "can't": true, "couldn't": true,
		"will": true,
	}

	// Check single words in the tail (up to 3 tokens back).
	tailStart := max(0, n-3)
	for i := tailStart; i < n; i++ {
		tok := tokens[i]
		if singleWordNegations[tok] {
			// Intent words ("cần", "muốn", "định", "nên") only negate if immediately preceding
			// and not followed by a past marker "đã" / "has" / "have".
			if tok == "cần" || tok == "muốn" || tok == "định" || tok == "nên" {
				hasPastMarker := false
				for j := i + 1; j < n; j++ {
					if tokens[j] == "đã" || tokens[j] == "has" || tokens[j] == "have" {
						hasPastMarker = true
						break
					}
				}
				if hasPastMarker {
					continue
				}
			}
			return true
		}
	}

	return false
}

// truncateForLog bounds a string for structured log output.
func truncateForLog(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
