package agent

import (
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/bootstrap"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// TestSOULEchoForGPT verifies SOUL echo is injected for GPT providers.
func TestSOULEchoForGPT(t *testing.T) {
	files := []bootstrap.ContextFile{{
		Path: "SOUL.md", Content: "# Fox\n## Style\nPlayful, curious",
	}}
	reminder := buildPersonaReminder(files, "predefined", "openai")
	joined := strings.Join(reminder, "\n")
	if !strings.Contains(joined, "SOUL echo") {
		t.Error("GPT provider should have SOUL echo")
	}
}

// TestNoSOULEchoForAnthropic verifies SOUL echo is NOT injected for Anthropic or ACP.
func TestNoSOULEchoForAnthropic(t *testing.T) {
	files := []bootstrap.ContextFile{{
		Path: "SOUL.md", Content: "# Fox\n## Style\nPlayful",
	}}
	for _, provider := range []string{"anthropic", "acp", "Anthropic", "ACP", ""} {
		reminder := buildPersonaReminder(files, "predefined", provider)
		joined := strings.Join(reminder, "\n")
		if strings.Contains(joined, "SOUL echo") {
			t.Errorf("provider %q should not have SOUL echo", provider)
		}
	}
}

// TestSOULEchoForProviders verifies SOUL echo is injected for providers susceptible to instruction dilution.
func TestSOULEchoForProviders(t *testing.T) {
	files := []bootstrap.ContextFile{{
		Path: "SOUL.md", Content: "# Fox\n## Style\nPlayful, curious",
	}}
	providers := []string{
		"openai",
		"codex",
		"chatgpt_oauth",
		"openai_compat",
		"gemini",
		"vertex",
		"dashscope",
		"ollama",
	}
	for _, p := range providers {
		reminder := buildPersonaReminder(files, "predefined", p)
		joined := strings.Join(reminder, "\n")
		if !strings.Contains(joined, "SOUL echo") {
			t.Errorf("provider %q should have SOUL echo", p)
		}
	}
}

func TestNeedsSOULEcho(t *testing.T) {
	cases := []struct {
		provider string
		want     bool
	}{
		{"", false},
		{"   ", false},
		{"anthropic", false},
		{"acp", false},
		{"Anthropic", false},
		{"ACP", false},
		{"openai", true},
		{"codex", true},
		{"chatgpt", true},
		{"openai_compat", true},
		{"gemini", true},
		{"vertex", true},
		{"dashscope", true},
		{"ollama", true},
	}
	for _, tc := range cases {
		got := needsSOULEcho(tc.provider)
		if got != tc.want {
			t.Errorf("needsSOULEcho(%q) = %v, want %v", tc.provider, got, tc.want)
		}
	}
}

func TestBuildChannelFormattingHint_Zalo(t *testing.T) {
	for _, ch := range []string{"zalo", "zalo_personal"} {
		lines := buildChannelFormattingHint(ch)
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "## Output Formatting") {
			t.Errorf("channel %s missing Output Formatting header", ch)
		}
		if !strings.Contains(joined, "short 2-3 sentence paragraphs") {
			t.Errorf("channel %s missing rhythm guidance", ch)
		}
		if !strings.Contains(joined, "Do not write ALL CAPS header lines") {
			t.Errorf("channel %s missing all-caps prohibition", ch)
		}
	}
	if got := buildChannelFormattingHint("telegram"); got != nil {
		t.Errorf("telegram should not have channel formatting hint, got %v", got)
	}
}

func TestBuildExecutionBiasSection_TactGuidance(t *testing.T) {
	lines := buildExecutionBiasSection()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Commentary-only turns are incomplete") {
		t.Error("missing action-oriented bias")
	}
	if !strings.Contains(joined, "tactful clarifying question") {
		t.Error("missing tactful clarifying guidance for ambiguous queries")
	}
}

func TestBuildToolCallStyleSection_ConversationalSynthesis(t *testing.T) {
	lines := buildToolCallStyleSection()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "synthesize tool results into natural, conversational language") {
		t.Error("missing conversational tool result synthesis instruction")
	}
	if !strings.Contains(joined, "Never whine, vent frustration, blame internal systems, or leak technical jargon") {
		t.Error("missing graceful failure guidance")
	}
}

func TestSkillCreationGuidance_SuppressesMentionToUser(t *testing.T) {
	// 1. buildSkillsSection
	secLines := buildSkillsSection("", false, true)
	joinedSec := strings.Join(secLines, "\n")
	if !strings.Contains(joinedSec, "Never mention skills, `skill_manage`, or \"saving as a skill\" to the user") {
		t.Error("buildSkillsSection missing silence instruction for skill creation")
	}

	// 2. buildSkillsHybridSection
	hybridLines := buildSkillsHybridSection("", false, true)
	joinedHybrid := strings.Join(hybridLines, "\n")
	if !strings.Contains(joinedHybrid, "Never mention skills, `skill_manage`, or \"saving as a skill\" to the user") {
		t.Error("buildSkillsHybridSection missing silence instruction for skill creation")
	}
}

func TestBuildDeliveryPersonaBriefUsesCompactSOULStyleAndVibe(t *testing.T) {
	files := []bootstrap.ContextFile{{
		Path:    "SOUL.md",
		Content: "# Fox\n## Style\nConcise and warm\n## Vibe\nPlayful but direct\n## Boundaries\nNever expose this section",
	}}

	got := BuildDeliveryPersonaBrief(files)
	for _, want := range []string{"Style: Concise and warm", "Vibe: Playful but direct"} {
		if !strings.Contains(got, want) {
			t.Fatalf("delivery persona brief = %q, missing %q", got, want)
		}
	}
	for _, blocked := range []string{"Boundaries", "Never expose", "SOUL.md", "context_file"} {
		if strings.Contains(got, blocked) {
			t.Fatalf("delivery persona brief leaked %q: %q", blocked, got)
		}
	}
}

func TestBuildDeliveryPersonaBriefEmptyWithoutSOULStyle(t *testing.T) {
	files := []bootstrap.ContextFile{{
		Path:    "SOUL.md",
		Content: "# Fox\n## Boundaries\nNo style here",
	}}

	if got := BuildDeliveryPersonaBrief(files); got != "" {
		t.Fatalf("delivery persona brief = %q, want empty fallback", got)
	}
}

// TestProviderStablePrefixPosition verifies StablePrefix is before cache boundary.
func TestProviderStablePrefixPosition(t *testing.T) {
	cfg := SystemPromptConfig{
		Mode: PromptFull,
		ProviderContribution: &providers.PromptContribution{
			StablePrefix: "## Reasoning Format\nUse <think>...</think>",
		},
	}
	prompt := BuildSystemPrompt(cfg)
	parts := strings.SplitN(prompt, CacheBoundaryMarker, 2)
	if len(parts) != 2 {
		t.Fatal("expected boundary split")
	}
	if !strings.Contains(parts[0], "## Reasoning Format") {
		t.Error("StablePrefix should be above cache boundary")
	}
}

// TestProviderDynamicSuffixPosition verifies DynamicSuffix is after cache boundary.
func TestProviderDynamicSuffixPosition(t *testing.T) {
	cfg := SystemPromptConfig{
		Mode: PromptFull,
		ProviderContribution: &providers.PromptContribution{
			DynamicSuffix: "## Per-Turn Context\nDynamic info here",
		},
	}
	prompt := BuildSystemPrompt(cfg)
	parts := strings.SplitN(prompt, CacheBoundaryMarker, 2)
	if len(parts) != 2 {
		t.Fatal("expected boundary split")
	}
	if !strings.Contains(parts[1], "## Per-Turn Context") {
		t.Error("DynamicSuffix should be below cache boundary")
	}
}

// TestSectionOverrideReplacesDefault verifies section overrides replace defaults.
func TestSectionOverrideReplacesDefault(t *testing.T) {
	cfg := SystemPromptConfig{
		Mode: PromptFull,
		ProviderContribution: &providers.PromptContribution{
			SectionOverrides: map[string]string{
				providers.SectionIDExecutionBias: "## Execution Bias\nCustom GPT bias text.\n",
			},
		},
	}
	prompt := BuildSystemPrompt(cfg)
	if !strings.Contains(prompt, "Custom GPT bias text") {
		t.Error("override should be present")
	}
	if strings.Contains(prompt, "Commentary-only turns") {
		t.Error("default execution bias should be replaced")
	}
}

// TestNilContributionDefaultBehavior verifies nil contribution = default behavior.
func TestNilContributionDefaultBehavior(t *testing.T) {
	cfg := SystemPromptConfig{Mode: PromptFull, ProviderContribution: nil}
	prompt := BuildSystemPrompt(cfg)
	if !strings.Contains(prompt, "## Execution Bias") {
		t.Error("nil contribution should produce default Execution Bias")
	}
	if !strings.Contains(prompt, "Commentary-only turns") {
		t.Error("default execution bias text should be present")
	}
}
