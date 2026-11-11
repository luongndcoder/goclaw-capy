package bootstrap

import (
	"strings"
	"testing"
)

func TestModeAllowlist(t *testing.T) {
	tests := []struct {
		mode string
		want map[string]bool
	}{
		{"full", nil},
		{"", nil},
		{"task", map[string]bool{AgentsTaskFile: true, ToolsFile: true, CapabilitiesFile: true, SoulFile: true, IdentityFile: true}},
		{"minimal", map[string]bool{AgentsCoreFile: true, CapabilitiesFile: true}},
		{"none", map[string]bool{ToolsFile: true}},
		{"unknown", nil}, // fail-open to full
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			got := ModeAllowlist(tt.mode)
			if tt.want == nil {
				if got != nil {
					t.Errorf("ModeAllowlist(%q) = %v, want nil", tt.mode, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ModeAllowlist(%q) = nil, want %v", tt.mode, tt.want)
			}
			if len(got) != len(tt.want) {
				t.Errorf("ModeAllowlist(%q) len = %d, want %d", tt.mode, len(got), len(tt.want))
			}
			for k := range tt.want {
				if !got[k] {
					t.Errorf("ModeAllowlist(%q) missing %q", tt.mode, k)
				}
			}
		})
	}
}

func TestTemplatesContainEQAndTactGuidance(t *testing.T) {
	// 1. SOUL.md
	soul, err := ReadTemplate(SoulFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", SoulFile, err)
	}
	for _, want := range []string{
		"## Conversational Tact",
		"3-Step Conversational Rhythm",
		"Acknowledge & Empathize",
		"Graceful Failure",
		"Never whine or blame internal systems",
		"Never leak internal technical jargon",
		"Natural Native Phrasing",
		"When replying in Vietnamese",
		"❌ BAD",
		"✅ GOOD",
	} {
		if !strings.Contains(soul, want) {
			t.Errorf("SOUL.md missing tact requirement: %q", want)
		}
	}

	// 2. AGENTS_CORE.md
	core, err := ReadTemplate(AgentsCoreFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", AgentsCoreFile, err)
	}
	for _, want := range []string{
		"Keep a natural, native tone",
		"Synthesize tool outputs into conversational",
	} {
		if !strings.Contains(core, want) {
			t.Errorf("AGENTS_CORE.md missing requirement: %q", want)
		}
	}

	// 3. AGENTS_TASK.md
	task, err := ReadTemplate(AgentsTaskFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", AgentsTaskFile, err)
	}
	for _, want := range []string{
		"Keep a natural, native tone",
		"Synthesize tool outputs into conversational",
	} {
		if !strings.Contains(task, want) {
			t.Errorf("AGENTS_TASK.md missing requirement: %q", want)
		}
	}
}
