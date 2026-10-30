package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/skills"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// newTestSkillLoader writes a single skill (name/SKILL.md) under a temp
// workspace and returns a Loader that resolves it via the flat
// <workspace>/skills/<name>/SKILL.md path (see skills.Loader.LoadSkill).
func newTestSkillLoader(t *testing.T, name, body string) *skills.Loader {
	t.Helper()
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: test skill\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return skills.NewLoader(workspace, "", "")
}

// TestUseSkillTool_NoReadFile_InlinesContent covers issue #1477: an agent
// whose resolved tool set does not include read_file gets the body inline
// (same as everyone now — single-turn invocation).
func TestUseSkillTool_NoReadFile_InlinesContent(t *testing.T) {
	loader := newTestSkillLoader(t, "ck-plan", "SKILL BODY CONTENT")
	tool := NewUseSkillTool(loader)

	restricted := map[string]bool{"web_search": true} // no read_file
	ctx := store.WithAvailableToolNames(context.Background(), restricted)

	res := tool.Execute(ctx, map[string]any{"name": "ck-plan"})
	if res == nil {
		t.Fatal("Execute returned nil result")
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "SKILL BODY CONTENT") {
		t.Fatalf("ForLLM must inline skill body, got %q", res.ForLLM)
	}
}

// TestUseSkillTool_AlwaysInlinesContent: skill invocation is a real tool —
// one call returns the instructions inline regardless of read_file access,
// so the model follows them without a follow-up turn.
func TestUseSkillTool_AlwaysInlinesContent(t *testing.T) {
	for _, ctx := range []context.Context{
		context.Background(), // nil allowlist
		store.WithAvailableToolNames(context.Background(), map[string]bool{"read_file": true, "write_file": true}),
		store.WithAvailableToolNames(context.Background(), map[string]bool{"web_search": true}), // no read_file
	} {
		loader := newTestSkillLoader(t, "ck-plan", "SKILL BODY CONTENT")
		tool := NewUseSkillTool(loader)

		res := tool.Execute(ctx, map[string]any{"name": "ck-plan"})
		if res == nil {
			t.Fatal("Execute returned nil result")
		}
		if res.IsError {
			t.Fatalf("unexpected error result: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "SKILL BODY CONTENT") {
			t.Fatalf("ForLLM must inline skill body, got %q", res.ForLLM)
		}
	}
}

// TestUseSkillTool_TruncatesOversizedContent: huge skills truncate with a
// read_file fallback pointer instead of burning context.
func TestUseSkillTool_TruncatesOversizedContent(t *testing.T) {
	big := strings.Repeat("x", maxInlineSkillChars+100)
	loader := newTestSkillLoader(t, "big-skill", big)
	tool := NewUseSkillTool(loader)

	res := tool.Execute(context.Background(), map[string]any{"name": "big-skill"})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %#v", res)
	}
	if !strings.Contains(res.ForLLM, "read_file") {
		t.Errorf("truncated skill must point at read_file fallback, got %.120q...", res.ForLLM)
	}
	if len([]rune(res.ForLLM)) > maxInlineSkillChars+2000 {
		t.Errorf("inlined content not bounded, len=%d", len([]rune(res.ForLLM)))
	}
}

// TestUseSkillTool_NoReadFile_SkillNotFound_ReturnsError covers the inlining
// path's failure mode: a restricted agent asking for a nonexistent skill must
// get a normal error result, not a panic or an empty inline.
func TestUseSkillTool_NoReadFile_SkillNotFound_ReturnsError(t *testing.T) {
	loader := newTestSkillLoader(t, "ck-plan", "SKILL BODY CONTENT")
	tool := NewUseSkillTool(loader)

	restricted := map[string]bool{"web_search": true}
	ctx := store.WithAvailableToolNames(context.Background(), restricted)

	res := tool.Execute(ctx, map[string]any{"name": "does-not-exist"})
	if res == nil {
		t.Fatal("Execute returned nil result")
	}
	if !res.IsError {
		t.Fatalf("expected IsError, got ForLLM = %q", res.ForLLM)
	}
}

// TestUseSkillTool_EmptyName_ReturnsError is unchanged pre-existing behavior.
func TestUseSkillTool_EmptyName_ReturnsError(t *testing.T) {
	tool := NewUseSkillTool(nil)
	res := tool.Execute(context.Background(), map[string]any{})
	if res == nil {
		t.Fatal("Execute returned nil result")
	}
	if !res.IsError {
		t.Fatalf("expected IsError for missing name, got ForLLM = %q", res.ForLLM)
	}
}
