package codex

import (
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
)

func TestCodexBuildTurnLinesPrependsBootstrapUser(t *testing.T) {
	lines, err := codexBuildTurnLines([]model.Message{
		{Role: model.RoleAssistant, Content: "prior work"},
		{Role: model.RoleUser, Content: "continue"},
	}, "/tmp/p", time.Unix(100, 0).UTC(), "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), codexRestoredUserPrompt("claude-code")) {
		t.Fatal("expected bootstrap user prompt")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "agent_message") {
		t.Fatal("expected agent_message")
	}
}

func TestCodexIsRestoredUserPromptRecognizesCurrentAndLegacyForms(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{codexRestoredUserPrompt("claude-code"), true},
		{codexRestoredUserPrompt(""), true},
		{"[Session restored via agenthop — continuing prior work]", true},
		{"[Session restored via another — continuing prior work]", true},
		{"a normal user message", false},
	}
	for _, c := range cases {
		if got := codexIsRestoredUserPrompt(c.text); got != c.want {
			t.Errorf("codexIsRestoredUserPrompt(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}
