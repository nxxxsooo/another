package util_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

func TestParseWorktreeList(t *testing.T) {
	// Built from real temp directories: drive-less literals like /repo only
	// occur in git output on Unix, while on Windows git prints a drive letter.
	a := filepath.Join(t.TempDir(), "repo")
	b := filepath.Join(t.TempDir(), "repo tree")
	data := []byte("worktree " + a + "\x00HEAD one\x00branch refs/heads/main\x00\x00worktree " + b + "\x00HEAD two\x00detached\x00\x00")
	want := []string{util.NormalizeProjectPath(a), util.NormalizeProjectPath(b)}
	if got := util.ParseWorktreeList(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
}

func TestDiscoverProjectScopeFromLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := filepath.Join(t.TempDir(), "main")
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "-q")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
	runGit(t, root, "worktree", "add", "-q", "-b", "linked-test", linked)
	if err := os.Mkdir(filepath.Join(linked, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	mainScope := util.DiscoverProjectScope(context.Background(), root)
	linkedScope := util.DiscoverProjectScope(context.Background(), filepath.Join(linked, "subdir"))
	if !mainScope.Git || !linkedScope.Git {
		t.Fatalf("expected git scopes: main=%+v linked=%+v", mainScope, linkedScope)
	}
	if !reflect.DeepEqual(mainScope.Worktrees, linkedScope.Worktrees) {
		t.Fatalf("worktrees differ: main=%#v linked=%#v", mainScope.Worktrees, linkedScope.Worktrees)
	}
	if len(mainScope.Worktrees) != 2 || mainScope.Root != util.NormalizeProjectPath(root) {
		t.Fatalf("scope = %+v", mainScope)
	}
}

func TestDiscoverProjectScopeOutsideGitFallsBackToCWD(t *testing.T) {
	dir := t.TempDir()
	scope := util.DiscoverProjectScope(context.Background(), dir)
	if scope.Git || scope.CWD != util.NormalizeProjectPath(dir) || scope.Root != scope.CWD || len(scope.Worktrees) != 0 {
		t.Fatalf("scope = %+v", scope)
	}
}

func TestProjectScopeRootsAndContains(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	tree := filepath.Join(base, "repo-feature")
	for _, dir := range []string{repo, tree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repo, tree = util.NormalizeProjectPath(repo), util.NormalizeProjectPath(tree)

	git := util.ProjectScope{CWD: repo, Root: repo, Worktrees: []string{repo, tree}, Git: true}
	if got := git.Roots(); !reflect.DeepEqual(got, []string{repo, tree}) {
		t.Fatalf("git roots = %#v", got)
	}
	plain := util.ProjectScope{CWD: repo, Root: repo}
	if got := plain.Roots(); !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("plain roots = %#v", got)
	}
	if got := (util.ProjectScope{}).Roots(); got != nil {
		t.Fatalf("empty scope roots = %#v, want nil", got)
	}

	for _, tc := range []struct {
		name  string
		scope util.ProjectScope
		path  string
		want  bool
	}{
		{"root itself", plain, repo, true},
		{"child", plain, filepath.Join(repo, "internal", "cli"), true},
		{"sibling sharing the prefix", plain, tree, false},
		{"traversal out of the root", plain, filepath.Join(repo, "..", "elsewhere"), false},
		{"parent", plain, base, false},
		{"empty path", plain, "", false},
		{"linked worktree child", git, filepath.Join(tree, "docs"), true},
		{"outside every worktree", git, filepath.Join(base, "other"), false},
	} {
		if got := tc.scope.Contains(tc.path); got != tc.want {
			t.Errorf("%s: Contains(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
