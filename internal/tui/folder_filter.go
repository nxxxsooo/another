package tui

import (
	"path/filepath"
	"strings"

	"github.com/nxxxsooo/another/internal/util"
)

// folderFilterPrefix is the only folder syntax inside `/` search.
const folderFilterPrefix = "path:"

// folderNoMatch is a ProjectExact sentinel that matches no session. It is
// used when a folder fragment is present but resolves to nothing inside the
// current scope, so the list goes honestly empty instead of leaking scope.
const folderNoMatch = "\x00another-no-match"

// parseFolderQuery splits a raw `/` input into its folder fragment and the
// remaining text query. Only the first path: token wins; the token itself is
// never part of the text query.
func parseFolderQuery(query string) (frag, text string) {
	var kept []string
	for _, tok := range strings.Fields(query) {
		if strings.HasPrefix(tok, folderFilterPrefix) {
			if frag == "" {
				frag = strings.Trim(strings.TrimPrefix(tok, folderFilterPrefix), `"'`)
			}
			continue
		}
		kept = append(kept, tok)
	}
	return frag, strings.Join(kept, " ")
}

// effectiveScopeRoots mirrors applyProjectScope without a query: the roots
// the current scope already covers.
func effectiveScopeRoots(scope util.ProjectScope, mode scopeMode) []string {
	if mode == scopeModeAll {
		return nil
	}
	if scope.Git && len(scope.Worktrees) > 0 {
		return append([]string(nil), scope.Worktrees...)
	}
	if scope.CWD != "" {
		return []string{scope.CWD}
	}
	return nil
}

func underAnyRoot(candidate string, roots []string) bool {
	for _, r := range roots {
		if candidate == r || strings.HasPrefix(candidate, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveFolderRoots turns a path: fragment into absolute scope roots. The
// fragment stays inside the current scope: an absolute fragment must sit
// under it, and a relative one is read against it. A bare worktree name (or
// worktree/rest) resolves worktree-first so path:edge finds the tree before
// deeper matching. A relative fragment that names no worktree fans out to
// every scope root. No filesystem access happens, so removed directories
// still filter. It returns nil when nothing inside the scope matches.
func resolveFolderRoots(scope util.ProjectScope, mode scopeMode, frag, cwd string) []string {
	frag = strings.TrimSpace(strings.Trim(frag, `"'`))
	if frag == "" {
		return nil
	}
	expanded := frag
	if strings.HasPrefix(frag, "~") {
		if e, err := util.ExpandDir(frag); err == nil && e != "" {
			expanded = e
		}
	}
	norm := func(p string) string { return util.NormalizeProjectPath(p) }
	// A typed fragment may use either slash; read it in native form so the
	// worktree split and the joins below behave on every platform.
	native := filepath.FromSlash(expanded)
	join := func(root, rel string) string { return norm(filepath.Join(root, filepath.FromSlash(rel))) }
	normalizeRoots := func(roots []string) []string {
		out := make([]string, 0, len(roots))
		for _, r := range roots {
			if n := norm(r); n != "" {
				out = append(out, n)
			}
		}
		return out
	}
	if mode == scopeModeAll {
		base := norm(scope.CWD)
		if base == "" {
			base = norm(cwd)
		}
		if filepath.IsAbs(native) {
			return []string{norm(native)}
		}
		if base == "" {
			return []string{norm(native)}
		}
		return []string{join(base, native)}
	}
	// Roots are normalized before any containment check: stored scope paths
	// and freshly joined candidates must be compared in the same form, or a
	// platform that spells absolute paths differently (drive letters,
	// symlinked tmp dirs) silently matches nothing.
	roots := normalizeRoots(effectiveScopeRoots(scope, mode))
	if len(roots) == 0 {
		return nil
	}
	if filepath.IsAbs(native) {
		n := norm(native)
		if underAnyRoot(n, roots) {
			return []string{n}
		}
		return nil
	}
	if scope.Git {
		first, rest, _ := strings.Cut(native, string(filepath.Separator))
		var matched []string
		for _, r := range roots {
			if filepath.Base(r) == first {
				if rest == "" {
					matched = append(matched, norm(r))
				} else {
					matched = append(matched, join(r, rest))
				}
			}
		}
		if len(matched) > 0 {
			return matched
		}
	}
	var out []string
	seen := make(map[string]bool)
	for _, r := range roots {
		c := join(r, expanded)
		if underAnyRoot(c, roots) && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
