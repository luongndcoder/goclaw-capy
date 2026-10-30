package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nextlevelbuilder/goclaw/internal/skills"
)

// UseSkillTool activates a skill and returns its instructions INLINE in a
// single call (one turn), following the harness pattern where skill
// invocation is a real tool rather than a marker: the model calls use_skill,
// gets the content, and follows it — no mandatory read_file follow-up turn.
// Oversized skills are truncated with a pointer to read_file as fallback.
type UseSkillTool struct {
	loader *skills.Loader
}

// maxInlineSkillChars bounds inlined skill content. Larger skills truncate
// with a read_file fallback note instead of burning context.
const maxInlineSkillChars = 12000

func NewUseSkillTool(loader *skills.Loader) *UseSkillTool { return &UseSkillTool{loader: loader} }

func (t *UseSkillTool) Name() string { return "use_skill" }

func (t *UseSkillTool) Description() string {
	return "Activate a skill and get its instructions inline. Call with the skill name, then follow the returned instructions directly."
}

func (t *UseSkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name or slug to activate",
			},
			"params": map[string]any{
				"type":        "object",
				"description": "Optional skill-specific parameters",
			},
		},
		"required": []string{"name"},
	}
}

func (t *UseSkillTool) Execute(ctx context.Context, args map[string]any) *Result {
	name, _ := args["name"].(string)
	if name == "" {
		return ErrorResult("name parameter is required")
	}
	if t.loader == nil {
		return ErrorResult(fmt.Sprintf("skill %q unavailable: no skill loader configured", name))
	}

	slog.Info("skill.activated", "skill", name)

	content, ok := t.loader.LoadSkill(ctx, name)
	if !ok {
		return ErrorResult(fmt.Sprintf("skill %q not found", name))
	}
	if n := len([]rune(content)); n > maxInlineSkillChars {
		content = string([]rune(content)[:maxInlineSkillChars]) +
			fmt.Sprintf("\n\n…[skill content truncated (%d chars total). Read the full SKILL.md with read_file for the remainder, then follow it.]", n)
	}
	return NewResult(fmt.Sprintf("<skill name=%q>\n%s\n</skill>\nFollow these instructions to complete the task.", name, content))
}
