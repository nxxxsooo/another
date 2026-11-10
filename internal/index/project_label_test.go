package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/util"
)

func TestProjectLabelPersistsAndSearchesWithoutChangingPathScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	sm := model.Summary{
		ID: "labeled", Provider: "codex", Title: "ordinary conversation", ProjectPath: root,
		ProjectLabel: "Film studio", StoragePath: filepath.Join(t.TempDir(), "rollout.jsonl"), UpdatedAt: time.Now(),
	}
	if err := store.Upsert(sm); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for _, get := range []func() (*model.Summary, error){
		func() (*model.Summary, error) { return store.Get("codex", sm.ID) },
		func() (*model.Summary, error) { return store.FindByID("label") },
	} {
		got, err := get()
		if err != nil || got.ProjectLabel != sm.ProjectLabel || got.ProjectPath != util.NormalizeProjectPath(root) {
			t.Fatalf("label/path round trip = %+v, %v", got, err)
		}
	}
	opts := ListOpts{Query: sm.ProjectLabel, ProjectExact: root}
	rows, err := store.List(opts)
	if err != nil || len(rows) != 1 || rows[0].ProjectLabel != sm.ProjectLabel {
		t.Fatalf("label list = %+v, %v", rows, err)
	}
	if count, err := store.Count(opts); err != nil || count != 1 {
		t.Fatalf("label count = %d, %v", count, err)
	}
	hits, err := store.Search(SearchOpts{Query: sm.ProjectLabel, ProjectExact: root})
	if err != nil || len(hits) != 1 || hits[0].Session.ProjectLabel != sm.ProjectLabel {
		t.Fatalf("label search = %+v, %v", hits, err)
	}
	if count, err := store.Count(ListOpts{ProjectFilter: sm.ProjectLabel}); err != nil || count != 0 {
		t.Fatalf("display label became a path filter: %d, %v", count, err)
	}
	// Metadata can disappear; rebuilding a canonical row must clear its old label.
	sm.ProjectLabel = ""
	if err := store.Upsert(sm); err != nil {
		t.Fatal(err)
	}
	if count, err := store.Count(opts); err != nil || count != 0 {
		t.Fatalf("removed label remains searchable: %d, %v", count, err)
	}
}

func TestCodexDisplayUpgradeRepairsNamedGuardianAndCachesProjectLabel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("CODEX_SQLITE_HOME", "")
	sessions := filepath.Join(home, "sessions")
	project := filepath.Join(home, ".chatgpt-projects", "g-p-example")
	for _, dir := range []string{sessions, project} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, name TEXT, rollout_path TEXT NOT NULL);
CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT);
CREATE TABLE project_roots (project_id TEXT, path TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects VALUES ('project', 'Film studio'); INSERT INTO project_roots VALUES ('project', ?)`, project); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"guardian", "user"} {
		var source any = "vscode"
		if id == "guardian" {
			source = map[string]any{"subagent": map[string]string{"other": "guardian"}}
		}
		meta, err := json.Marshal(map[string]any{
			"timestamp": "2026-01-01T00:00:00Z", "type": "session_meta",
			"payload": map[string]any{"id": id, "cwd": project, "source": source},
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(sessions, "rollout-"+id+".jsonl")
		body := string(meta) + "\n" + `{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"review this action"}}` + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO threads VALUES (?, 'Guardian review', ?)`, id, path); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	reg := registry.New()
	if _, err := Rebuild(context.Background(), reg, store, "codex"); err != nil {
		t.Fatal(err)
	}
	// Simulate the old reader's cache, with unchanged source fingerprints.
	if _, err := store.db.Exec(`UPDATE sessions SET kind='root', project_label=''; UPDATE session_sources SET kind='root', project_label=''`); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta(codexWireSchemaKey, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta("codex_wire_schema_v2", "previous reader"); err != nil {
		t.Fatal(err)
	}
	if !codexRebuildOwed(store) {
		t.Fatal("the previous reader marker incorrectly settled the display upgrade")
	}
	if _, err := UpdateIncremental(context.Background(), reg, store, "codex"); err != nil {
		t.Fatal(err)
	}
	rows, err := store.List(ListOpts{Provider: "codex"})
	if err != nil || len(rows) != 1 || rows[0].ID != "user" || rows[0].ProjectLabel != "Film studio" {
		t.Fatalf("default upgraded rows = %+v, %v", rows, err)
	}
	hits, err := store.Search(SearchOpts{Provider: "codex", Query: "Guardian review"})
	if err != nil || len(hits) != 1 || hits[0].Session.ID != "user" {
		t.Fatalf("default search leaked Guardian: %+v, %v", hits, err)
	}
	if count, err := store.Count(ListOpts{Provider: "codex", IncludeSubagents: true}); err != nil || count != 2 {
		t.Fatalf("include-subagents count = %d, %v", count, err)
	}
	if _, err := db.Exec(`UPDATE projects SET name='Renamed studio'`); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateIncremental(context.Background(), reg, store, "codex"); err != nil {
		t.Fatal(err)
	}
	rows, err = store.List(ListOpts{Query: "Renamed studio"})
	if err != nil || len(rows) != 1 || rows[0].ProjectLabel != "Renamed studio" {
		t.Fatalf("metadata-only rename stayed stale: %+v, %v", rows, err)
	}
}
