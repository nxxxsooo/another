package codex

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
)

func TestBuildV2RolloutLines(t *testing.T) {
	conv := &model.Conversation{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello"},
			{Role: model.RoleAssistant, Content: "world"},
		},
	}
	lines, err := buildV2RolloutLines(conv, "019d0304-afe0-7001-b42e-69d2028e34d1", "/tmp/p", time.Unix(100, 0).UTC(), "0.1.0", "openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 4 {
		t.Fatalf("lines = %d", len(lines))
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatal(err)
	}
	if row["type"] != "session_meta" {
		t.Fatalf("type = %v", row["type"])
	}
	payload, ok := row["payload"].(map[string]any)
	if !ok || payload["id"] != "019d0304-afe0-7001-b42e-69d2028e34d1" {
		t.Fatalf("bad session_meta payload: %v", row["payload"])
	}
	if !strings.Contains(lines[1], "task_started") {
		t.Fatalf("expected task_started, got %s", lines[1])
	}
	foundAgent := false
	for _, line := range lines {
		if strings.Contains(line, `"type":"agent_message"`) || strings.Contains(line, `"type": "agent_message"`) {
			foundAgent = true
			break
		}
	}
	if !foundAgent {
		t.Fatal("expected agent_message event")
	}
}
