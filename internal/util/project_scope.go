package util

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// ProjectScope describes the paths that belong to the project containing CWD.
// Git worktrees that share one repository are one project. Outside Git, the
// caller should use CWD as an exact directory filter.
type ProjectScope struct {
	CWD       string
	Root      string
	Worktrees []string
	Git       bool
}

// DiscoverProjectScope reads Git's registered worktree set without changing it.
// A missing Git executable and a non-repository directory both degrade to an
// exact-CWD scope; they are not startup errors for another.
func DiscoverProjectScope(ctx context.Context, cwd string) ProjectScope {
	cwd = NormalizeProjectPath(cwd)
	scope := ProjectScope{CWD: cwd, Root: cwd}
	if cwd == "" {
		return scope
	}
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "worktree", "list", "--porcelain", "-z")
	out, err := cmd.Output()
	if err != nil {
		return scope
	}
	roots := ParseWorktreeList(out)
	if len(roots) == 0 {
		return scope
	}
	scope.Git = true
	scope.Root = roots[0]
	scope.Worktrees = roots
	return scope
}

// ParseWorktreeList extracts worktree paths from `git worktree list
// --porcelain -z`. The NUL format also supports paths containing newlines.
func ParseWorktreeList(data []byte) []string {
	const prefix = "worktree "
	seen := make(map[string]bool)
	var roots []string
	for _, field := range bytes.Split(data, []byte{0}) {
		s := string(field)
		if !strings.HasPrefix(s, prefix) {
			continue
		}
		root := NormalizeProjectPath(strings.TrimPrefix(s, prefix))
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}
