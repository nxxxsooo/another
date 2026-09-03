package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
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
	if !strings.Contains(lines[1], `"type":"another_migration"`) {
		t.Fatalf("expected migration marker immediately after session_meta, got %s", lines[1])
	}
	if !strings.Contains(lines[2], "task_started") {
		t.Fatalf("expected task_started, got %s", lines[2])
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

func TestV2RolloutWhitespaceRoundTrip(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	conv := &model.Conversation{Messages: []model.Message{
		{Role: model.RoleUser, Content: "  hello\r\n", Timestamp: now},
		{Role: model.RoleAssistant, Content: "\tworld  ", Timestamp: now.Add(time.Second)},
	}}
	lines, err := buildV2RolloutLines(conv, "019d0304-afe0-7001-b42e-69d2028e34d1", "/tmp/p", now, "0.1.0", "openai")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := (&Provider{}).Load(context.Background(), provider.SessionRef{StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContentDigest(loaded) != model.ContentDigest(conv) || loaded.Messages[0].Content != conv.Messages[0].Content || loaded.Messages[1].Content != conv.Messages[1].Content {
		t.Fatalf("Codex whitespace changed: %#v", loaded.Messages)
	}
}

func TestV2RolloutPreservesAlreadyLoadedUserAssistantText(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	conv := &model.Conversation{Messages: []model.Message{
		{Role: model.RoleUser, Content: "first", Timestamp: now},
		{Role: model.RoleUser, Content: "<environment_context>keep after provider load</environment_context>", Timestamp: now.Add(time.Second)},
		{Role: model.RoleAssistant, Content: "[CONTEXT COMPACTION] keep exact assistant text", Timestamp: now.Add(2 * time.Second)},
	}}
	lines, err := buildV2RolloutLines(conv, "019d0304-afe0-7001-b42e-69d2028e34d1", "/tmp/p", now, "0.1.0", "openai")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := (&Provider{}).Load(context.Background(), provider.SessionRef{StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContentDigest(loaded) != model.ContentDigest(conv) || len(loaded.Messages) != len(conv.Messages) {
		t.Fatalf("Codex dropped loaded conversation text: %#v", loaded.Messages)
	}
}
