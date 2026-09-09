package index_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

// stubProvider is one agent's store as the index sees it: something that
// either answers a scan or fails one.
type stubProvider struct {
	id        string
	summaries []model.Summary
	err       error
}

func (s *stubProvider) ID() string          { return s.id }
func (s *stubProvider) DisplayName() string { return s.id }
func (s *stubProvider) Installed() bool     { return true }
func (s *stubProvider) DefaultPaths() []provider.PathSpec {
	return nil
}

func (s *stubProvider) Discover(context.Context, provider.DiscoverOpts) ([]model.Summary, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.summaries, nil
}

func (s *stubProvider) Load(context.Context, provider.SessionRef) (*model.Conversation, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) Write(context.Context, *model.Conversation, provider.WriteOpts) (*provider.WriteResult, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) SupportsResume() bool                      { return false }
func (s *stubProvider) ResumeCommand(provider.WriteResult) string { return "" }

// TestUpdateIncrementalIndexesAgentsAfterOneThatCannotBeRead is the first-run
// failure: a store another cannot read — an OpenCode database its own agent
// has since migrated, an empty Hermes state.db — used to end the pass where it
// happened, so every agent registered after it stayed at zero sessions while
// setup still showed its CLI as installed.
func TestUpdateIncrementalIndexesAgentsAfterOneThatCannotBeRead(t *testing.T) {
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	broken := &stubProvider{id: "opencode", err: errors.New("no such table: session")}
	working := &stubProvider{id: "pi", summaries: []model.Summary{
		{ID: "pi-1", Provider: "pi", StoragePath: "/native/pi-1", Title: "still indexed"},
	}}
	reg := registry.NewWith(broken, working)

	n, err := index.UpdateIncremental(context.Background(), reg, store, "")
	if n != 1 {
		t.Fatalf("indexed %d sessions, want the readable agent's one", n)
	}
	var failure *index.ScanFailure
	if !errors.As(err, &failure) || len(failure.Errors) != 1 {
		t.Fatalf("err = %v, want one reported scan failure", err)
	}
	counts, err := store.CountByProvider()
	if err != nil {
		t.Fatal(err)
	}
	if counts["pi"] != 1 {
		t.Fatalf("counts = %v, want the agent after the failure indexed", counts)
	}
}

// TestRebuildIndexesAgentsAfterOneThatCannotBeRead is the same contract for a
// full rebuild, which is what a person runs to recover from exactly this.
func TestRebuildIndexesAgentsAfterOneThatCannotBeRead(t *testing.T) {
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	broken := &stubProvider{id: "hermes", err: errors.New("no such table: sessions")}
	working := &stubProvider{id: "qwen", summaries: []model.Summary{
		{ID: "qwen-1", Provider: "qwen", StoragePath: "/native/qwen-1", Title: "still indexed"},
	}}
	reg := registry.NewWith(broken, working)

	n, err := index.Rebuild(context.Background(), reg, store, "")
	if n != 1 {
		t.Fatalf("indexed %d sessions, want the readable agent's one", n)
	}
	var failure *index.ScanFailure
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want a reported scan failure", err)
	}
	counts, err := store.CountByProvider()
	if err != nil {
		t.Fatal(err)
	}
	if counts["qwen"] != 1 {
		t.Fatalf("counts = %v, want the agent after the failure indexed", counts)
	}
}
