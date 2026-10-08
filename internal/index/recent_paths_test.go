package index_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/util"
)

// The relocate box offers directories people actually work in, most recent
// first, one row per directory however many sessions it holds.
func TestRecentProjectPathsOrdersByLatestActivity(t *testing.T) {
	store := openTestStore(t)
	base := time.Now().Add(-time.Hour)
	put := func(id, project string, age time.Duration) {
		t.Helper()
		at := base.Add(-age)
		if err := store.Upsert(model.Summary{
			ID: id, Provider: "pi", Title: id, ProjectPath: project,
			CreatedAt: at, UpdatedAt: at, MessageCount: 1,
			StoragePath: "/s/" + id, SourceMtime: at.UnixNano(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("a1", "/p/old", 3*time.Hour)
	put("b1", "/p/new", time.Minute)
	put("c1", "/p/mid", time.Hour)
	put("a2", "/p/old", 2*time.Second) // a recent session lifts its directory
	put("e1", "", 0)

	got, err := store.RecentProjectPaths(10)
	if err != nil {
		t.Fatal(err)
	}
	// The index stores paths normalized, which on Windows makes /p/old into
	// a drive-qualified path.
	want := []string{
		util.NormalizeProjectPath("/p/old"), util.NormalizeProjectPath("/p/new"), util.NormalizeProjectPath("/p/mid"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got, err = store.RecentProjectPaths(2)
	if err != nil || len(got) != 2 {
		t.Fatalf("limit 2 = %v, %v", got, err)
	}
}
