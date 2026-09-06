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
	data := []byte("worktree /repo\x00HEAD one\x00branch refs/heads/main\x00\x00worktree /tmp/repo tree\x00HEAD two\x00detached\x00\x00")
	want := []string{"/repo", "/tmp/repo tree"}
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
