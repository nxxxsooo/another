package codex

import (
	"strings"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
)

func TestCodexBuildTurnLinesPrependsBootstrapUser(t *testing.T) {
	lines, err := codexBuildTurnLines([]model.Message{
		{Role: model.RoleAssistant, Content: "prior work"},
		{Role: model.RoleUser, Content: "continue"},
	}, "/tmp/p", time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), codexRestoredUserPrompt) {
		t.Fatal("expected bootstrap user prompt")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "agent_message") {
		t.Fatal("expected agent_message")
	}
}
