package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
)

// A child is hidden from the session list because its parent leads back to it.
// When the parent is gone that reasoning fails, and hiding the child only makes
// it unreachable. A child that names no parent — Codex's guardian threads — is
// not a lost child and stays hidden.
func TestOrphanedChildIsListedAndReturnsToChildWhenParentAppears(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	base := model.Summary{
		Provider: "codex", ProjectPath: "/demo", UpdatedAt: now, MessageCount: 3,
		SourceMtime: 1, SourceSize: 10,
	}
	orphan := base
	orphan.ID = "orphan"
	orphan.Title = "api_definitions · Wegener the 9th"
	orphan.StoragePath = "/codex/orphan.jsonl"
	orphan.Kind = model.SessionKindSubagent
	orphan.ParentID = "vanished-parent"

	guardian := base
	guardian.ID = "guardian"
	guardian.Title = "guardian assessment"
	guardian.StoragePath = "/codex/guardian.jsonl"
	guardian.Kind = model.SessionKindSubagent

	if err := store.reconcileProvider("codex", []model.Summary{orphan, guardian}, map[string]struct{}{
		orphan.StoragePath: {}, guardian.StoragePath: {},
	}); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List(ListOpts{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "orphan" {
		t.Fatalf("default list = %+v, want only the parentless child whose parent is named", listed)
	}
	if listed[0].ParentID != "vanished-parent" {
		t.Fatalf("promotion erased provenance: parent = %q", listed[0].ParentID)
	}

	// The parent turning up restores the hierarchy without another rebuild.
	parent := base
	parent.ID = "vanished-parent"
	parent.Title = "the parent thread"
	parent.StoragePath = "/codex/parent.jsonl"
	if err := store.reconcileProvider("codex", []model.Summary{parent}, map[string]struct{}{
		orphan.StoragePath: {}, guardian.StoragePath: {}, parent.StoragePath: {},
	}); err != nil {
		t.Fatal(err)
	}
	listed, err = store.List(ListOpts{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "vanished-parent" {
		t.Fatalf("default list = %+v, want the child hidden again under its parent", listed)
	}
	all, err := store.List(ListOpts{Provider: "codex", IncludeSubagents: true})
	if err != nil || len(all) != 3 {
		t.Fatalf("full list = %d rows err=%v", len(all), err)
	}
}
