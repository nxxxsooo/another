package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

// fakeProvider keeps each session as one JSON file under root, which is enough
// for the index to fingerprint it, for migration to reload and verify what it
// wrote, and for dedup to check that the target still exists.
type fakeProvider struct {
	id        string
	root      string
	installed bool
	mu        sync.Mutex
	writes    int
	renamed   map[string]string
	relocated []provider.RelocateOpts
}

func newFake(t *testing.T, id string) *fakeProvider {
	t.Helper()
	return &fakeProvider{id: id, root: t.TempDir(), installed: true, renamed: map[string]string{}}
}

func (f *fakeProvider) path(id string) string { return filepath.Join(f.root, id+".json") }

func (f *fakeProvider) seed(t *testing.T, conv model.Conversation) {
	t.Helper()
	conv.Provider = f.id
	conv.StoragePath = f.path(conv.ID)
	conv.MessageCount = len(conv.Messages)
	f.save(t, &conv)
}

func (f *fakeProvider) save(t *testing.T, conv *model.Conversation) {
	t.Helper()
	if err := f.write(conv); err != nil {
		t.Fatal(err)
	}
}

// fakeRecord is the on-disk shape: the conversation plus the migration marker a
// real provider would embed in its native format. Conversation.Migration is
// json:"-", so it has to travel beside the conversation to survive a reload.
type fakeRecord struct {
	Conversation model.Conversation   `json:"conversation"`
	Migration    *model.MigrationMeta `json:"migration,omitempty"`
}

func (f *fakeProvider) write(conv *model.Conversation) error {
	rec := fakeRecord{Conversation: *conv, Migration: conv.Migration}
	if conv.WriteMigration != nil {
		rec.Migration = conv.WriteMigration
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(f.path(conv.ID), data, 0o600)
}

func (f *fakeProvider) read(id string) (*model.Conversation, error) {
	data, err := os.ReadFile(f.path(id))
	if os.IsNotExist(err) {
		return nil, provider.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var rec fakeRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	conv := rec.Conversation
	conv.Migration = rec.Migration
	conv.Provider = f.id
	conv.StoragePath = f.path(id)
	conv.MessageCount = len(conv.Messages)
	return &conv, nil
}

func (f *fakeProvider) ID() string           { return f.id }
func (f *fakeProvider) DisplayName() string  { return "Fake " + f.id }
func (f *fakeProvider) Installed() bool      { return f.installed }
func (f *fakeProvider) SupportsResume() bool { return true }
func (f *fakeProvider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "sessions", Path: f.root}}
}
func (f *fakeProvider) ResumeCommand(r provider.WriteResult) string {
	return fmt.Sprintf("%s --resume %s", f.id, r.SessionID)
}

func (f *fakeProvider) Discover(_ context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	entries, err := os.ReadDir(f.root)
	if err != nil {
		return nil, err
	}
	var out []model.Summary
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if opts.SkipSource != nil && opts.SkipSource(f.path(id), info.ModTime().UnixNano(), info.Size()) {
			continue
		}
		conv, err := f.read(id)
		if err != nil {
			return nil, err
		}
		out = append(out, model.Summary{
			ID: conv.ID, Provider: f.id, ProjectPath: conv.ProjectPath, Title: conv.Title,
			CreatedAt: conv.CreatedAt, UpdatedAt: conv.UpdatedAt, MessageCount: len(conv.Messages),
			StoragePath: f.path(id), Kind: "root",
			SourceMtime: info.ModTime().UnixNano(), SourceSize: info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeProvider) Load(_ context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	return f.read(ref.ID)
}

func (f *fakeProvider) Write(_ context.Context, conv *model.Conversation, opts provider.WriteOpts) (*provider.WriteResult, error) {
	f.mu.Lock()
	f.writes++
	id := fmt.Sprintf("%s-w%d", f.id, f.writes)
	f.mu.Unlock()
	project := opts.ProjectPath
	if project == "" {
		project = conv.ProjectPath
	}
	res := &provider.WriteResult{SessionID: id, StoragePath: f.path(id), ProjectPath: project}
	if opts.DryRun {
		return res, nil
	}
	copy := *conv
	copy.ID, copy.Provider, copy.ProjectPath = id, f.id, project
	copy.Messages = append([]model.Message(nil), conv.Messages...)
	if err := f.write(&copy); err != nil {
		return nil, err
	}
	return res, nil
}

func (f *fakeProvider) RenameSession(_ context.Context, ref provider.SessionRef, title string) error {
	conv, err := f.read(ref.ID)
	if err != nil {
		return err
	}
	conv.Title = title
	conv.UpdatedAt = conv.UpdatedAt.Add(time.Second)
	f.mu.Lock()
	f.renamed[ref.ID] = title
	f.mu.Unlock()
	return f.write(conv)
}

func (f *fakeProvider) SupportsRelocate(provider.RelocateMode) bool { return true }

func (f *fakeProvider) RelocateSession(_ context.Context, ref provider.SessionRef, opts provider.RelocateOpts) (*provider.RelocateResult, error) {
	f.mu.Lock()
	f.relocated = append(f.relocated, opts)
	f.mu.Unlock()
	conv, err := f.read(ref.ID)
	if err != nil {
		return nil, err
	}
	if opts.Mode == provider.RelocateMove {
		res := &provider.RelocateResult{SessionID: conv.ID, StoragePath: f.path(conv.ID), ProjectPath: opts.Directory, Moved: true}
		if opts.DryRun {
			return res, nil
		}
		conv.ProjectPath = opts.Directory
		return res, f.write(conv)
	}
	id := conv.ID + "-fork"
	res := &provider.RelocateResult{SessionID: id, StoragePath: f.path(id), ProjectPath: opts.Directory}
	if opts.DryRun {
		return res, nil
	}
	conv.ID, conv.ProjectPath = id, opts.Directory
	return res, f.write(conv)
}

// bareProvider has only the core contract, so "does not support" paths are
// reachable in tests.
type bareProvider struct{ f *fakeProvider }

func (b bareProvider) ID() string                                  { return b.f.ID() }
func (b bareProvider) DisplayName() string                         { return b.f.DisplayName() }
func (b bareProvider) DefaultPaths() []provider.PathSpec           { return b.f.DefaultPaths() }
func (b bareProvider) Installed() bool                             { return b.f.Installed() }
func (b bareProvider) SupportsResume() bool                        { return b.f.SupportsResume() }
func (b bareProvider) ResumeCommand(r provider.WriteResult) string { return b.f.ResumeCommand(r) }
func (b bareProvider) Discover(ctx context.Context, o provider.DiscoverOpts) ([]model.Summary, error) {
	return b.f.Discover(ctx, o)
}
func (b bareProvider) Load(ctx context.Context, r provider.SessionRef) (*model.Conversation, error) {
	return b.f.Load(ctx, r)
}
func (b bareProvider) Write(ctx context.Context, c *model.Conversation, o provider.WriteOpts) (*provider.WriteResult, error) {
	return b.f.Write(ctx, c, o)
}

var (
	seedCreated = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedUpdated = time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
)

func sampleConversation(id, project, title string) model.Conversation {
	return model.Conversation{
		ID: id, ProjectPath: project, Title: title, CreatedAt: seedCreated, UpdatedAt: seedUpdated,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "the stripe webhook retries forever", Timestamp: seedCreated},
			{Role: model.RoleAssistant, Content: "the handler returns 500 on duplicates; make it idempotent", Timestamp: seedCreated.Add(time.Minute)},
			{Role: model.RoleUser, Content: "ship it", Timestamp: seedUpdated},
		},
	}
}

// newTestApp wires the CLI to a temporary index and the given providers, with
// configuration, cache, stdin, and current-session environment isolated from
// the machine running the tests.
func newTestApp(t *testing.T, providers ...provider.Provider) *App {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, v := range []string{"PI_SESSION_ID", "CLAUDE_SESSION_ID", "CODEX_THREAD_ID", "OPENCODE_SESSION_ID", "ANTIGRAVITY_CONVERSATION_ID"} {
		t.Setenv(v, "")
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = devnull
	t.Cleanup(func() { os.Stdin = oldStdin; _ = devnull.Close() })
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	reg := registry.NewWith(providers...)
	return &App{Registry: reg, Index: idx, Migrate: &migrate.Engine{Registry: reg, Index: idx}}
}

// run executes one CLI invocation and returns what it printed to stdout. The
// commands print with fmt directly, so stdout itself is redirected.
func run(t *testing.T, app *App, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&buf, r); close(done) }()
	root := app.Root()
	root.SetArgs(args)
	execErr := root.Execute()
	_ = w.Close()
	os.Stdout = old
	<-done
	_ = r.Close()
	return buf.String(), execErr
}

func mustRun(t *testing.T, app *App, args ...string) string {
	t.Helper()
	out, err := run(t, app, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

func wantContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Fatalf("output missing %q:\n%s", w, out)
		}
	}
}

func wantErr(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Fatalf("error = %v, want %q", err, substr)
	}
}

// captureStdout runs fn and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&buf, r); close(done) }()
	fn()
	_ = w.Close()
	os.Stdout = old
	<-done
	_ = r.Close()
	return buf.String()
}
