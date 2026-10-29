package util

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProjectScope describes the paths that belong to the project containing CWD.
// Git worktrees that share one repository are one project; outside Git the
// project is CWD and everything under it.
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

// Roots is what "this project" covers in the index: every registered worktree
// of a repository, or a plain directory and everything under it. Nil means the
// scope has no anchor and reads as all projects.
func (s ProjectScope) Roots() []string {
	if s.Git && len(s.Worktrees) > 0 {
		return append([]string(nil), s.Worktrees...)
	}
	if s.CWD == "" {
		return nil
	}
	return []string{s.CWD}
}

// Contains reports whether a recorded project path sits at or below one of the
// scope's roots, by the same rule the index applies to ProjectRoots. A
// sibling sharing a name prefix (/repo-old beside /repo) is outside.
func (s ProjectScope) Contains(projectPath string) bool {
	p := NormalizeProjectPath(projectPath)
	if p == "" {
		return false
	}
	sep := string(filepath.Separator)
	for _, root := range s.Roots() {
		root = NormalizeProjectPath(root)
		if root == "" {
			continue
		}
		prefix := root
		if !strings.HasSuffix(prefix, sep) {
			prefix += sep
		}
		if p == root || strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
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
