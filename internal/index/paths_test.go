package index_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/util"
)

func openTestStore(t *testing.T) *index.Store {
	t.Helper()
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func upsertSession(t *testing.T, store *index.Store, id, project, storage string) {
	t.Helper()
	now := time.Now()
	if err := store.Upsert(model.Summary{
		ID: id, Provider: "claude-code", Title: id, ProjectPath: project,
		CreatedAt: now, UpdatedAt: now, MessageCount: 2,
		StoragePath: storage, SourceMtime: now.UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
}

// Claude Code keeps one storage folder per working directory and renames that
// folder when the directory moves, so the folder ends up holding sessions from
// both the old and the new path. That is checkable evidence of the move, and
// the proposed alias must be the shortest prefix that expresses it.
func TestMissingDirectoriesProposeTheColocatedMove(t *testing.T) {
	store := openTestStore(t)
	live := filepath.Join(t.TempDir(), "Work", "fit", "projects", "fit-link")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	live = util.NormalizeProjectPath(live)
	gone := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(live)))), "Projects", "fit", "fit-link")
	folder := filepath.Join(t.TempDir(), "projects", "-encoded-folder")
	upsertSession(t, store, "moved", gone, filepath.Join(folder, "moved.jsonl"))
	upsertSession(t, store, "after", live, filepath.Join(folder, "after.jsonl"))

	missing, err := store.MissingDirectories()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Path != gone || missing[0].Sessions != 1 {
		t.Fatalf("missing = %+v, want one row for %s", missing, gone)
	}
	if len(missing[0].Candidates) == 0 {
		t.Fatalf("no candidate proposed for %s", gone)
	}
	candidate := missing[0].Candidates[0]
	if candidate.Path != live {
		t.Fatalf("candidate = %q, want %q", candidate.Path, live)
	}
	wantFrom := filepath.Dir(gone)
	wantTo := filepath.Dir(live)
	if candidate.Alias.From != wantFrom || candidate.Alias.To != wantTo {
		t.Fatalf("alias = %+v, want %s -> %s", candidate.Alias, wantFrom, wantTo)
	}
}

// A directory whose project genuinely disappeared has nothing to propose, and
// another must say so instead of inventing a destination.
func TestMissingDirectoryWithoutEvidenceProposesNothing(t *testing.T) {
	store := openTestStore(t)
	gone := filepath.Join(t.TempDir(), "deleted-project")
	upsertSession(t, store, "orphan", gone, filepath.Join(t.TempDir(), "store", "orphan.jsonl"))
	missing, err := store.MissingDirectories()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || len(missing[0].Candidates) != 0 {
		t.Fatalf("missing = %+v, want one row with no candidate", missing)
	}
}

// An alias is a decision about where a project lives now. It moves the
// sessions another shows, leaves the directory the agent recorded intact, and
// is undone by dropping it — with no provider file read in either direction.
func TestPathAliasFollowsAMovedProjectAndUnlinkRestoresIt(t *testing.T) {
	store := openTestStore(t)
	root := util.NormalizeProjectPath(t.TempDir())
	newDir := filepath.Join(root, "Work", "fit", "projects", "fitlink")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(root, "Projects", "fit", "fitlink")
	upsertSession(t, store, "s1", oldDir, filepath.Join(root, "store", "s1.jsonl"))

	alias := config.PathAlias{From: filepath.Join(root, "Projects", "fit"), To: filepath.Join(root, "Work", "fit", "projects")}
	store.SetPathAliases([]config.PathAlias{alias})
	if err := store.ReprojectSessions(); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(index.ListOpts{ProjectRoots: []string{newDir}, IncludeSubagents: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("aliased list: items=%d err=%v", len(items), err)
	}
	if items[0].ProjectPath != newDir {
		t.Fatalf("ProjectPath = %q, want %q", items[0].ProjectPath, newDir)
	}

	store.SetPathAliases(nil)
	if err := store.ReprojectSessions(); err != nil {
		t.Fatal(err)
	}
	items, err = store.List(index.ListOpts{IncludeSubagents: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("restored list: items=%d err=%v", len(items), err)
	}
	if items[0].ProjectPath != oldDir {
		t.Fatalf("ProjectPath = %q, want the recorded %q back", items[0].ProjectPath, oldDir)
	}
}

// A prefix alias must stop at a path separator. /root/Projects/fit owns
// /root/Projects/fit/api, never the unrelated /root/Projects/fitx.
func TestPathAliasStopsAtPathSegmentBoundaries(t *testing.T) {
	store := openTestStore(t)
	root := util.NormalizeProjectPath(t.TempDir())
	inside := filepath.Join(root, "Projects", "fit", "api")
	sibling := filepath.Join(root, "Projects", "fitx")
	upsertSession(t, store, "inside", inside, filepath.Join(root, "store", "inside.jsonl"))
	upsertSession(t, store, "sibling", sibling, filepath.Join(root, "store", "sibling.jsonl"))

	store.SetPathAliases([]config.PathAlias{{
		From: filepath.Join(root, "Projects", "fit"),
		To:   filepath.Join(root, "Work", "fit"),
	}})
	if err := store.ReprojectSessions(); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(index.ListOpts{IncludeSubagents: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, item := range items {
		got[item.ID] = item.ProjectPath
	}
	if want := filepath.Join(root, "Work", "fit", "api"); got["inside"] != want {
		t.Fatalf("inside ProjectPath = %q, want %q", got["inside"], want)
	}
	if got["sibling"] != sibling {
		t.Fatalf("sibling ProjectPath = %q, want the untouched %q", got["sibling"], sibling)
	}
}
