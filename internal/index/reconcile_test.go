package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
)

func TestReconcilePromotesRemainingSourceAndPrunesDeletedSession(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	transcript := model.Summary{
		ID: "same", Provider: "cursor", Title: "transcript", StoragePath: "/cursor/transcript.jsonl",
		UpdatedAt: now.Add(time.Hour), SourceMtime: 1, SourceSize: 10,
	}
	native := transcript
	native.Title = "native"
	native.StoragePath = "/cursor/store.db"
	native.UpdatedAt = now
	native.SourcePriority = 10
	if err := store.reconcileProvider("cursor", []model.Summary{transcript, native}, map[string]struct{}{
		transcript.StoragePath: {}, native.StoragePath: {},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("cursor", "same")
	if err != nil || got.StoragePath != native.StoragePath {
		t.Fatalf("native source not selected: got=%+v err=%v", got, err)
	}
	if err := store.IndexConversation(*got, &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "stale winner text"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileProvider("cursor", nil, map[string]struct{}{transcript.StoragePath: {}}); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get("cursor", "same")
	if err != nil || got.StoragePath != transcript.StoragePath {
		t.Fatalf("remaining source not promoted: got=%+v err=%v", got, err)
	}
	status, err := store.ContentStatus()
	if err != nil || status.Pending != 1 || status.Indexed != 0 {
		t.Fatalf("promoted source must need content reindex: status=%+v err=%v", status, err)
	}
	hits, err := store.Search(SearchOpts{Query: "stale winner text"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("old winner content survived promotion: hits=%+v err=%v", hits, err)
	}
	if err := store.reconcileProvider("cursor", nil, map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("cursor", "same"); err == nil {
		t.Fatal("session survived after all physical sources disappeared")
	}
}

func TestReconcileInvalidatesSameTimestampAndCountSourceEdit(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sm := model.Summary{
		ID: "edited", Provider: "codex", Title: "same", StoragePath: "/codex/session.jsonl",
		UpdatedAt: time.Unix(100, 0), MessageCount: 2, SourceMtime: 1000, SourceSize: 20,
	}
	seen := map[string]struct{}{sm.StoragePath: {}}
	if err := store.reconcileProvider(sm.Provider, []model.Summary{sm}, seen); err != nil {
		t.Fatal(err)
	}
	if err := store.IndexConversation(sm, &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "old fingerprint text"}}}); err != nil {
		t.Fatal(err)
	}
	sm.SourceMtime++
	sm.SourceSize++
	if err := store.reconcileProvider(sm.Provider, []model.Summary{sm}, seen); err != nil {
		t.Fatal(err)
	}
	status, err := store.ContentStatus()
	if err != nil || status.Pending != 1 || status.Indexed != 0 {
		t.Fatalf("edited source status=%+v err=%v", status, err)
	}
	hits, err := store.Search(SearchOpts{Query: "old fingerprint text"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("stale content survived source edit: hits=%+v err=%v", hits, err)
	}
}
