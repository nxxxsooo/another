package util

import (
	"os"
	"path/filepath"
	"strings"
)

// NestedRepoRoots returns the Git repository roots that sit strictly below base
// and enclose at least one of candidates.
//
// A plain folder scopes to itself and everything under it, because agents record
// the directory they ran in rather than its parent. A descendant that is its own
// repository is the exception: it is already its own project everywhere else in
// another, so absorbing it would file one repo's sessions under an unrelated
// folder's name. Opening another in a folder that merely contains checkouts
// would otherwise report every one of them as "this project".
//
// Only candidates are examined, never the filesystem at large: the paths come
// from the index, so this costs a bounded walk per distinct project directory
// instead of a scan of the whole tree.
func NestedRepoRoots(base string, candidates []string) []string {
	base = NormalizeProjectPath(base)
	if base == "" {
		return nil
	}
	prefix := base + "/"
	isRoot := make(map[string]bool)
	found := make(map[string]bool)
	var roots []string
	for _, candidate := range candidates {
		dir := NormalizeProjectPath(candidate)
		if dir == "" || !strings.HasPrefix(dir, prefix) {
			continue
		}
		// Walk up toward base and stop at the nearest enclosing repository: a
		// checkout inside a checkout belongs to the inner one. base itself is
		// never tested, because it is the scope, not something to exclude.
		for strings.HasPrefix(dir, prefix) {
			ok, cached := isRoot[dir]
			if !cached {
				// .git is a directory in a normal checkout and a file in a
				// linked worktree or submodule; both mark a project boundary.
				_, err := os.Stat(filepath.Join(dir, ".git"))
				ok = err == nil
				isRoot[dir] = ok
			}
			if ok {
				if !found[dir] {
					found[dir] = true
					roots = append(roots, dir)
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return roots
}
