package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxxxsooo/another/internal/util"
)

// suggestKind says where a relocate suggestion came from, so the row can say
// why it is there.
type suggestKind int

const (
	suggestDir suggestKind = iota
	suggestWorktree
	suggestProject
)

type pathSuggestion struct {
	path string
	kind suggestKind
}

// suggestSources is everything suggestPaths reads besides the typed text.
// worktrees and recent are loaded once when the box opens; the directory
// listing happens per keystroke.
type suggestSources struct {
	home      string
	cwd       string
	worktrees []string
	recent    []string
	// exclude is the normalized directory the session already lives in.
	exclude string
}

// relocateSuggestLimit is the most rows the box shows; fewer when the
// terminal has no room for them.
const relocateSuggestLimit = 6

// suggestPaths offers destinations for the typed text: worktrees of the
// launch repository, then directories completing the last path segment, then
// directories the index has seen. Only directories that exist are offered,
// each once.
func suggestPaths(typed string, src suggestSources, limit int) []pathSuggestion {
	var out []pathSuggestion
	seen := map[string]bool{}
	add := func(p string, kind suggestKind) {
		if len(out) >= limit {
			return
		}
		key := util.NormalizeProjectPath(p)
		if key == "" || seen[key] || key == src.exclude {
			return
		}
		seen[key] = true
		out = append(out, pathSuggestion{path: p, kind: kind})
	}
	// Worktrees lead but may not take every row: a repository with many of
	// them would otherwise hide all other destinations. The rest backfill
	// whatever room the other sources leave.
	var worktrees []string
	for _, p := range src.worktrees {
		if matchesTyped(p, typed, src.home) && isDirectory(p) {
			worktrees = append(worktrees, p)
		}
	}
	lead := min(len(worktrees), max(1, limit-2))
	for _, p := range worktrees[:lead] {
		add(p, suggestWorktree)
	}
	if strings.TrimSpace(typed) != "" {
		for _, p := range completeDirectory(typed, src) {
			add(p, suggestDir)
		}
	}
	for _, p := range src.recent {
		if matchesTyped(p, typed, src.home) && isDirectory(p) {
			add(p, suggestProject)
		}
	}
	for _, p := range worktrees[lead:] {
		add(p, suggestWorktree)
	}
	return out
}

// matchesTyped is a case-insensitive substring match against the path as
// shown (with ~) and as stored, so both "feat" and a pasted absolute path find
// a worktree.
func matchesTyped(p, typed, home string) bool {
	typed = strings.ToLower(strings.TrimSpace(typed))
	if typed == "" {
		return true
	}
	return strings.Contains(strings.ToLower(tildeFor(p, home)), typed) ||
		strings.Contains(strings.ToLower(p), typed)
}

// completeDirectory lists subdirectories of the typed directory whose name
// starts with the typed last segment. A trailing separator lists every child.
// Hidden directories appear only once the segment starts with a dot.
func completeDirectory(typed string, src suggestSources) []string {
	expanded := strings.TrimSpace(typed)
	if expanded == "~" {
		expanded = "~" + string(filepath.Separator)
	}
	if strings.HasPrefix(expanded, "~/") || strings.HasPrefix(expanded, "~"+string(filepath.Separator)) {
		expanded = src.home + expanded[1:]
	} else if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(src.cwd, expanded) + trailingSep(expanded)
	}
	dir, prefix := filepath.Dir(expanded), filepath.Base(expanded)
	if strings.HasSuffix(expanded, string(filepath.Separator)) || strings.HasSuffix(expanded, "/") {
		dir, prefix = filepath.Clean(expanded), ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	lowerPrefix := strings.ToLower(prefix)
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
			continue
		}
		symlinkedDir := e.Type()&os.ModeSymlink != 0 && isDirectory(filepath.Join(dir, name))
		if !e.IsDir() && !symlinkedDir {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = filepath.Join(dir, name)
	}
	return out
}

func trailingSep(p string) string {
	if strings.HasSuffix(p, "/") || strings.HasSuffix(p, string(filepath.Separator)) {
		return string(filepath.Separator)
	}
	return ""
}

func isDirectory(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// tildeFor is util.TildePath against an explicit home, so the shown form can
// be tested without touching the real one. It only replaces a whole leading
// segment: /home/al never becomes ~ for a home of /home/a.
func tildeFor(p, home string) string {
	if home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
