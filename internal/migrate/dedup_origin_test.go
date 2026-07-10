package migrate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/providers/codex"
)

func TestFindDuplicateByOriginWhenDigestChanges(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sessions", "2026", "06", "28")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	origin := &model.Conversation{
		ID: "20260628_151621_cd7bdb", Provider: "hermes",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	meta := model.NewMigrationMeta(origin)
	path := filepath.Join(sub, "rollout-2026-06-28-019f0f20.jsonl")
	lines := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"019f0f20"}}`,
		`{"type":"agenthop_migration","data":` + mustJSON(meta) + `}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", dir)

	changed := &model.Conversation{
		ID: "20260628_151621_cd7bdb", Provider: "hermes",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello"},
			{Role: model.RoleAssistant, Content: "filtered tool noise"},
		},
	}
	wr, ok := migrate.FindDuplicate(nil, codex.New(), changed)
	if !ok {
		t.Fatal("expected duplicate by origin id")
	}
	if wr.SessionID != "019f0f20" {
		t.Fatalf("session id = %q", wr.SessionID)
	}
}
