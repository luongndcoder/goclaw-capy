package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// TestClaimsWriteCompletion_CoversRealWorldPhrasings guards the exact gap
// users reported: the model says "xong rồi / hoàn thành / đã xử lý" after a
// read-only call, and the old substring list missed it.
func TestClaimsWriteCompletion_CoversRealWorldPhrasings(t *testing.T) {
	t.Parallel()
	mustClaim := []string{
		"Em đã cập nhật sheet rồi anh nhé",
		"Xong rồi anh, đã xử lý xong",
		"Đã hoàn thành việc nhập liệu",
		"Hoàn tất đồng bộ dữ liệu",
		"Đã lưu file thành công",
		"The sheet has been updated successfully",
		"Data exported to the report",
	}
	for _, s := range mustClaim {
		if !claimsWriteCompletion(s) {
			t.Errorf("expected claim detected: %q", s)
		}
	}
	mustNotClaim := []string{
		"",
		"Đây là dữ liệu em đọc được từ sheet",
		"Anh cho em biết range cần sửa nhé",
	}
	for _, s := range mustNotClaim {
		if claimsWriteCompletion(s) {
			t.Errorf("false positive on read-only text: %q", s)
		}
	}
}

// TestObserveStage_FabricationGuard_BlocksWithoutWrite verifies the guard
// blocks a completion claim when no write tool succeeded.
func TestObserveStage_FabricationGuard_BlocksWithoutWrite(t *testing.T) {
	t.Parallel()
	deps := &PipelineDeps{}
	stage := NewObserveStage(deps)
	st := defaultState()
	st.Ctx = context.Background()
	st.Think.LastResponse = &providers.ChatResponse{Content: "Xong rồi anh, em đã xử lý xong"}
	if err := stage.Execute(t.Context(), st); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !st.Observe.ContinueAfterFinal {
		t.Fatal("fabrication guard must block unverified completion claim")
	}
	if st.Observe.FinalContent != "" {
		t.Fatalf("blocked content must not become final, got %q", st.Observe.FinalContent)
	}
}

// TestObserveStage_AsyncGuard_BlocksClaimWithOutstanding verifies fix 5: a
// completion claim while async spawns are unretrieved is blocked with a
// spawn-get instruction instead of reaching the user.
func TestObserveStage_AsyncGuard_BlocksClaimWithOutstanding(t *testing.T) {
	t.Parallel()
	stage := NewObserveStage(&PipelineDeps{})
	st := defaultState()
	st.Ctx = context.Background()
	st.Tool.AsyncOutstanding = 2
	st.Think.LastResponse = &providers.ChatResponse{Content: "Hoàn thành rồi anh nhé"}
	if err := stage.Execute(t.Context(), st); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !st.Observe.ContinueAfterFinal {
		t.Fatal("async guard must block unverified completion claim")
	}
	if st.Observe.FinalContent != "" {
		t.Fatalf("blocked content must not become final, got %q", st.Observe.FinalContent)
	}
	pending := st.Messages.Pending()
	if len(pending) != 2 {
		t.Fatalf("expected assistant + retry instruction, got %d messages", len(pending))
	}
	if !strings.Contains(pending[1].Content, "spawn(action=\"get\"") {
		t.Errorf("retry must point at spawn get:\n%s", pending[1].Content)
	}
}

// TestObserveStage_AsyncGuard_PassesHonestProgress ensures progress reports
// without a completion claim still reach the user while work is outstanding.
func TestObserveStage_AsyncGuard_PassesHonestProgress(t *testing.T) {
	t.Parallel()
	stage := NewObserveStage(&PipelineDeps{})
	st := defaultState()
	st.Ctx = context.Background()
	st.Tool.AsyncOutstanding = 1
	st.Think.LastResponse = &providers.ChatResponse{Content: "Em đã giao việc cho subagent, có kết quả em báo anh ngay"}
	if err := stage.Execute(t.Context(), st); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if st.Observe.ContinueAfterFinal {
		t.Fatal("honest progress report must not be blocked")
	}
	if st.Observe.FinalContent == "" {
		t.Fatal("honest progress report must become final content")
	}
}
