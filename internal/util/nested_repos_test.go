package util

import (
	"os"
	"path/filepath"
	"testing"
)

// tempBase is t.TempDir() as NestedRepoRoots will report it. On macOS the
// temp root is a symlink (/var -> /private/var) and NormalizeProjectPath
// resolves it, so an unnormalized expectation never matches the result.
func tempBase(t *testing.T) string {
	t.Helper()
	return NormalizeProjectPath(t.TempDir())
}

// mkRepo marks dir as a checkout the way NestedRepoRoots detects one.
func mkRepo(t *testing.T, dir string, asFile bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(dir, ".git")
	if asFile {
		// A linked worktree and a submodule both record .git as a file.
		if err := os.WriteFile(git, []byte("gitdir: elsewhere\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.MkdirAll(git, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestNestedRepoRootsSeparatesCheckoutsFromPlainFolders(t *testing.T) {
	base := tempBase(t)
	mkRepo(t, filepath.Join(base, "GitHub/another"), false)
	mkRepo(t, filepath.Join(base, "Work/fit/projects/fit-match"), true)
	if err := os.MkdirAll(filepath.Join(base, "Docs/health"), 0o755); err != nil {
		t.Fatal(err)
	}

	candidates := []string{
		filepath.Join(base, "GitHub/another"),
		filepath.Join(base, "GitHub/another/internal/tui"), // inside a checkout
		filepath.Join(base, "Work/fit"),                    // plain
		filepath.Join(base, "Work/fit/projects/fit-match"),
		filepath.Join(base, "Docs/health"), // plain
		base,                               // the scope itself is never excluded
	}
	got := NestedRepoRoots(base, candidates)

	want := map[string]bool{
		filepath.Join(base, "GitHub/another"):              true,
		filepath.Join(base, "Work/fit/projects/fit-match"): true,
	}
	if len(got) != len(want) {
		t.Fatalf("roots = %v, want exactly %v", got, want)
	}
	for _, root := range got {
		if !want[root] {
			t.Errorf("unexpected excluded root %q", root)
		}
	}
}

// The folder rule this subtraction sits on top of must survive it: a plain
// subtree stays part of its parent folder's project, which is the whole point
// of scoping a non-Git folder to its descendants.
func TestNestedRepoRootsLeavesPlainSubtreesAlone(t *testing.T) {
	base := tempBase(t)
	deep := filepath.Join(base, "projects/ai-pioneer")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := NestedRepoRoots(base, []string{deep}); len(got) != 0 {
		t.Fatalf("roots = %v, want none for a plain subtree", got)
	}
}

// A checkout inside a checkout belongs to the inner one, so the walk stops at
// the nearest boundary instead of reporting both.
func TestNestedRepoRootsStopsAtNearestCheckout(t *testing.T) {
	base := tempBase(t)
	outer := filepath.Join(base, "outer")
	inner := filepath.Join(outer, "vendor/inner")
	mkRepo(t, outer, false)
	mkRepo(t, inner, false)

	got := NestedRepoRoots(base, []string{inner})
	if len(got) != 1 || got[0] != inner {
		t.Fatalf("roots = %v, want just %q", got, inner)
	}
}

func TestNestedRepoRootsIgnoresPathsOutsideBase(t *testing.T) {
	base := tempBase(t)
	other := tempBase(t)
	mkRepo(t, filepath.Join(other, "elsewhere"), false)

	if got := NestedRepoRoots(base, []string{filepath.Join(other, "elsewhere")}); len(got) != 0 {
		t.Fatalf("roots = %v, want none outside base", got)
	}
	if got := NestedRepoRoots("", []string{other}); got != nil {
		t.Fatalf("roots = %v, want nil for an empty base", got)
	}
}
