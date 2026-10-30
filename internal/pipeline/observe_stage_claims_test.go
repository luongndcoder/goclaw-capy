package pipeline

import (
	"context"
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
