package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
)

type corruptingProvider struct {
	path    string
	cleaned bool
	loaded  *model.Conversation
}

func (p *corruptingProvider) ID() string                        { return "corrupt" }
func (p *corruptingProvider) DisplayName() string               { return "Corrupt" }
func (p *corruptingProvider) DefaultPaths() []provider.PathSpec { return nil }
func (p *corruptingProvider) Installed() bool                   { return true }
func (p *corruptingProvider) SupportsResume() bool              { return false }
func (p *corruptingProvider) ResumeCommand(provider.WriteResult) string {
	return ""
}
func (p *corruptingProvider) Discover(context.Context, provider.DiscoverOpts) ([]model.Summary, error) {
	return nil, nil
}
func (p *corruptingProvider) Write(_ context.Context, _ *model.Conversation, _ provider.WriteOpts) (*provider.WriteResult, error) {
	if err := os.WriteFile(p.path, []byte("partial"), 0o600); err != nil {
		return nil, err
	}
	return &provider.WriteResult{SessionID: "target", StoragePath: p.path}, nil
}
func (p *corruptingProvider) Load(context.Context, provider.SessionRef) (*model.Conversation, error) {
	if p.loaded != nil {
		return p.loaded, nil
	}
	return &model.Conversation{
		ID: "target", Provider: p.ID(),
		Messages:  []model.Message{{Role: model.RoleUser, Content: "corrupt"}},
		Migration: &model.MigrationMeta{Type: model.MigrationType},
	}, nil
}
func (p *corruptingProvider) CleanupWrite(_ context.Context, _ provider.WriteResult) error {
	p.cleaned = true
	return os.Remove(p.path)
}

func TestEngineCleansArtifactAfterVerificationFailure(t *testing.T) {
	source := &model.Conversation{
		ID: "source", Provider: "source-provider",
		Messages: []model.Message{{Role: model.RoleUser, Content: "original"}},
	}
	dst := &corruptingProvider{path: filepath.Join(t.TempDir(), "target.jsonl")}
	_, err := (&Engine{}).writeConversation(context.Background(), dst, source, "", false)
	if err == nil || !strings.Contains(err.Error(), "content digest mismatch") {
		t.Fatalf("verification error = %v", err)
	}
	if !dst.cleaned {
		t.Fatal("verification failure did not invoke exact cleanup")
	}
	if _, err := os.Stat(dst.path); !os.IsNotExist(err) {
		t.Fatalf("failed target remains: %v", err)
	}
}

func TestVerifyWriteDetectsTimestampCorruptionAndAllowsUnspecifiedZero(t *testing.T) {
	ts := time.Date(2026, 8, 17, 1, 2, 3, 456000000, time.UTC)
	source := &model.Conversation{ID: "source", Provider: "source", Messages: []model.Message{{Role: model.RoleUser, Content: "same", Timestamp: ts}}}
	dst := &corruptingProvider{path: filepath.Join(t.TempDir(), "unused")}
	dst.loaded = &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "same", Timestamp: ts.Add(time.Millisecond)}}, Migration: ptrMigration(model.NewMigrationMeta(source))}
	if err := verifyWrite(context.Background(), dst, source, provider.WriteResult{SessionID: "target", StoragePath: dst.path}); err == nil || !strings.Contains(err.Error(), "content digest mismatch") {
		t.Fatalf("timestamp corruption error = %v", err)
	}
	source.Messages[0].Timestamp = time.Time{}
	dst.loaded.Migration = ptrMigration(model.NewMigrationMeta(source))
	if err := verifyWrite(context.Background(), dst, source, provider.WriteResult{SessionID: "target", StoragePath: dst.path}); err != nil {
		t.Fatalf("zero timestamp should be unspecified: %v", err)
	}
}

func ptrMigration(meta model.MigrationMeta) *model.MigrationMeta { return &meta }

func TestResumableConversationBoundsAndNormalizesProviderHistory(t *testing.T) {
	source := &model.Conversation{ID: "long", Provider: "cursor", Title: "Long task"}
	source.Messages = append(source.Messages, model.Message{Role: model.RoleUser, Content: "<user_info>provider setup</user_info>"})
	for i := 0; i < 200; i++ {
		source.Messages = append(source.Messages,
			model.Message{Role: model.RoleUser, Content: fmt.Sprintf("<timestamp>now</timestamp><user_query>prompt %d</user_query>", i)},
			model.Message{Role: model.RoleAssistant, Content: strings.Repeat("answer ", 1000)},
		)
	}
	source.Messages = append(source.Messages,
		model.Message{Role: model.RoleUser, Content: "<timestamp>now</timestamp><user_query>continue correctly</user_query>"},
		model.Message{Role: model.RoleAssistant, Content: "retry status"},
		model.Message{Role: model.RoleUser, Content: "<timestamp>now</timestamp><user_query>continue correctly</user_query>"},
		model.Message{Role: model.RoleUser, Content: "<dynamic_tools>provider setup</dynamic_tools>"},
	)

	projected, warning := resumableConversation(source)
	if warning == "" || len(projected.Messages) > resumeMessageLimit+1 {
		t.Fatalf("projection len=%d warning=%q", len(projected.Messages), warning)
	}
	if !strings.HasPrefix(projected.Messages[0].Content, "[Agenthop migration handoff]") {
		t.Fatalf("missing handoff: %q", projected.Messages[0].Content)
	}
	last := projected.Messages[len(projected.Messages)-1]
	if last.Content != "continue correctly" {
		t.Fatalf("last prompt = %q", last.Content)
	}
	var continueCount int
	for _, message := range projected.Messages {
		if message.Content == "continue correctly" {
			continueCount++
		}
	}
	if continueCount != 1 {
		t.Fatalf("duplicate prompt count = %d", continueCount)
	}
	meta := model.NewMigrationMeta(source)
	projected.WriteMigration = &meta
	writtenMeta := model.NewMigrationMeta(projected)
	if writtenMeta.OriginDigest != model.SnapshotDigest(source) || writtenMeta.OriginMessageCount != len(source.Messages) {
		t.Fatalf("projection lost origin identity: %+v", writtenMeta)
	}
}

func TestImportReturnsBookkeepingWarnings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	if err := os.MkdirAll(filepath.Join(root, "claude", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	engine := &Engine{Registry: registry.New(), Index: store}
	conv := &model.Conversation{
		ID: "portable", Provider: "export",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := engine.Import(ctx, conv, Options{ToProvider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "update index:") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	if _, err := os.Stat(res.Write.StoragePath); err != nil {
		t.Fatalf("successful artifact missing: %v", err)
	}
}

func TestImportDoesNotWriteWhenDedupIndexFails(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	if err := os.MkdirAll(filepath.Join(claudeRoot, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{Registry: registry.New(), Index: store}
	conv := &model.Conversation{
		ID: "portable", Provider: "export",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	if _, err := engine.Import(context.Background(), conv, Options{ToProvider: "claude"}); err == nil || !strings.Contains(err.Error(), "check existing migration") {
		t.Fatalf("dedup index error = %v", err)
	}
	var files []string
	_ = filepath.WalkDir(filepath.Join(claudeRoot, "projects"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}
		return err
	})
	if len(files) != 0 {
		t.Fatalf("import wrote despite dedup failure: %v", files)
	}
}
