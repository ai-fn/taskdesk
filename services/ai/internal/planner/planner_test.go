package planner

import (
	"strings"
	"testing"
)

// TestSystemPromptUnchanged guards against accidentally interpolating language
// into the cached system prompt — that would bust the Claude prompt cache prefix
// for every locale switch, eliminating the ~$0.30/call cache saving.
func TestSystemPromptUnchanged(t *testing.T) {
	const wantContains = "You are a daily task prioritization assistant"
	if !strings.Contains(systemPrompt, wantContains) {
		t.Errorf("systemPrompt no longer starts with expected text; cache-busting regression?")
	}
	// No language placeholder should appear in the system prompt.
	for _, marker := range []string{"{language}", "{{language}}", "Simplified Chinese", "简体中文"} {
		if strings.Contains(systemPrompt, marker) {
			t.Errorf("systemPrompt contains language marker %q — this busts the Claude cache prefix", marker)
		}
	}
}

func TestLanguageDirectiveMap(t *testing.T) {
	cases := []struct {
		lang      string
		wantLang  string // substring expected in the directive
	}{
		{"en", "English"},
		{"zh", "Simplified Chinese"},
		{"",   "English"},   // empty falls back to en
		{"fr", "English"},   // unsupported falls back to en
		{"zh-CN", "English"}, // not in map directly — Normalize handles this upstream
	}

	for _, tc := range cases {
		got := languageDirective(tc.lang)
		if !strings.Contains(got, tc.wantLang) {
			t.Errorf("languageDirective(%q) = %q, want substring %q", tc.lang, got, tc.wantLang)
		}
		// The directive must never be empty.
		if got == "" {
			t.Errorf("languageDirective(%q) returned empty string", tc.lang)
		}
	}
}
