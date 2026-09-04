package titler_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
)

// TestLiveBatch runs a real 10-session batch against the local index. It only
// suggests unless LIVE_BATCH_APPLY=1, in which case it renames the first
// three changed rows and reads them back: renaming real sessions on every
// invocation would make this unkeepable.
func TestLiveBatch(t *testing.T) {
	if os.Getenv("LIVE_BATCH") == "" {
		t.Skip("set LIVE_BATCH=1 to run a real batch against the local index")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	idx, err := index.Open(config.IndexPath())
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	reg := registry.New()
	p, err := reg.Get("pi")
	if err != nil {
		t.Fatalf("pi provider: %v", err)
	}

	summaries, err := idx.List(index.ListOpts{Provider: "pi", Limit: 60})
	if err != nil {
		t.Fatalf("list pi sessions: %v", err)
	}
	var items []titler.BatchItem
	var byID = map[string]model.Summary{}
	for _, sm := range summaries {
		if len(items) >= 10 {
			break
		}
		if sm.CreatedAt.Unix() <= 0 {
			continue
		}
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		var conv *model.Conversation
		if preview, ok := p.(provider.PreviewLoader); ok {
			conv, err = preview.LoadPreview(ctx, ref, 12)
		} else {
			conv, err = p.Load(ctx, ref)
		}
		if err != nil || conv == nil || len(conv.Messages) == 0 {
			continue
		}
		byID[sm.ID] = sm
		items = append(items, titler.BatchItem{SessionID: sm.ID, Request: titler.Request{
			Title: sm.Title, ProjectPath: sm.ProjectPath, CreatedAt: sm.CreatedAt, Messages: conv.Messages,
		}})
	}
	if len(items) == 0 {
		t.Fatal("no suggestible pi sessions found")
	}

	start := time.Now()
	var results []titler.BatchResult
	for r := range titler.SuggestBatch(ctx, titler.Config{Provider: "pi"}, items, titler.DefaultConcurrency) {
		results = append(results, r)
		t.Logf("row %s -> %q (frozen=%q err=%v)", r.SessionID, r.Title, r.Frozen, r.Err)
	}
	results = titler.FreezeDuplicates(results)
	counts := titler.Summarize(results)
	t.Logf("suggested %d rows in %s: changed=%d unchanged=%d frozen=%d failed=%d",
		len(results), time.Since(start).Round(time.Second), counts.Changed, counts.Unchanged, counts.Frozen, counts.Failed)
	if len(results) != len(items) {
		t.Fatalf("lost rows: got %d of %d", len(results), len(items))
	}

	if os.Getenv("LIVE_BATCH_APPLY") != "1" {
		t.Log("LIVE_BATCH_APPLY!=1: suggestions only, nothing renamed")
		return
	}
	renamer, ok := p.(provider.SessionRenamer)
	if !ok {
		t.Fatal("pi does not support rename")
	}
	applied := 0
	var appliedIDs []string
	for _, r := range results {
		if applied >= 3 || !r.Changed() {
			continue
		}
		sm := byID[r.SessionID]
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		if err := renamer.RenameSession(ctx, ref, r.Title); err != nil {
			t.Fatalf("rename %s: %v", r.SessionID, err)
		}
		applied++
		appliedIDs = append(appliedIDs, r.SessionID)
		t.Logf("renamed %s: %q -> %q", r.SessionID, r.Current, r.Title)
	}
	if applied == 0 {
		t.Fatal("nothing changed, nothing to apply")
	}
	if _, err := index.UpdateIncremental(ctx, reg, idx, "pi"); err != nil {
		t.Fatalf("index refresh: %v", err)
	}
	// Only renamed rows can read back changed; the rest still hold their old
	// titles until someone confirms them.
	want := map[string]string{}
	for _, r := range results {
		want[r.SessionID] = r.Title
	}
	for _, id := range appliedIDs {
		back, err := idx.FindByID(id)
		if err != nil {
			t.Fatalf("reread %s: %v", id, err)
		}
		if back.Title != want[id] {
			t.Fatalf("reread mismatch %s: index=%q want=%q", id, back.Title, want[id])
		}
	}
}
