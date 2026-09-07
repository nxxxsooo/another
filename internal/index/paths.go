package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/util"
)

// maxColocatedDirectories is how many distinct working directories a provider
// storage folder may hold before it stops being evidence of a move.
const maxColocatedDirectories = 3

// minTrailingSegments is how much of a path two directories must end in
// before a shared name counts as evidence on its own. One segment is a
// coincidence: Projects/fit/opencode and .local/share/opencode share nothing
// but a common word.
const minTrailingSegments = 2

// MissingDirectory is a directory the index still attributes sessions to,
// which no longer exists on disk. A renamed or relocated project produces one
// of these, and its sessions are invisible from the project's new home.
type MissingDirectory struct {
	Path       string
	Sessions   int
	Providers  []string
	Candidates []DirectoryCandidate
}

// DirectoryCandidate is where a missing directory's project may live now,
// together with the evidence that suggested it. another proposes; a person
// decides.
type DirectoryCandidate struct {
	Path  string
	Alias config.PathAlias
	Why   string
}

// MissingDirectories lists indexed directories that are gone from disk.
func (s *Store) MissingDirectories() ([]MissingDirectory, error) {
	rows, err := s.db.Query(`SELECT project_path, provider, COUNT(*)
FROM sessions WHERE project_path <> '' GROUP BY project_path, provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type agg struct {
		sessions  int
		providers map[string]struct{}
	}
	missing := map[string]*agg{}
	var live []string
	seenLive := map[string]bool{}
	for rows.Next() {
		var path, providerID string
		var n int
		if err := rows.Scan(&path, &providerID, &n); err != nil {
			return nil, err
		}
		if dirExists(path) {
			if !seenLive[path] {
				seenLive[path] = true
				live = append(live, path)
			}
			continue
		}
		entry := missing[path]
		if entry == nil {
			entry = &agg{providers: map[string]struct{}{}}
			missing[path] = entry
		}
		entry.sessions += n
		entry.providers[providerID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	colocated, err := s.colocatedDirectories()
	if err != nil {
		return nil, err
	}
	out := make([]MissingDirectory, 0, len(missing))
	for path, entry := range missing {
		providers := make([]string, 0, len(entry.providers))
		for id := range entry.providers {
			providers = append(providers, id)
		}
		sort.Strings(providers)
		out = append(out, MissingDirectory{
			Path: path, Sessions: entry.sessions, Providers: providers,
			Candidates: directoryCandidates(path, live, colocated[path]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// MissingDirectoriesFor narrows the report to directories that plausibly held
// this project before it moved: they end in the same path segments as one of
// the scope's roots, or a candidate for them lands inside the scope.
func (s *Store) MissingDirectoriesFor(roots []string) ([]MissingDirectory, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	all, err := s.MissingDirectories()
	if err != nil {
		return nil, err
	}
	var out []MissingDirectory
	for _, dir := range all {
		if relatedToRoots(dir, roots) {
			out = append(out, dir)
		}
	}
	return out, nil
}

func relatedToRoots(dir MissingDirectory, roots []string) bool {
	for _, root := range roots {
		if sharedTrailingSegments(dir.Path, root) >= minTrailingSegments {
			return true
		}
		for _, candidate := range dir.Candidates {
			if candidate.Path == root || strings.HasPrefix(candidate.Path, root+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// colocatedDirectories maps each indexed directory to the other directories
// whose sessions share a provider storage folder with it.
//
// Agents that keep one folder per working directory rewrite that folder's name
// when the directory moves, so the folder ends up holding both the sessions
// recorded before the move and the ones recorded after. That makes the move
// readable straight from the index, with no file re-read and no guessing.
func (s *Store) colocatedDirectories() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT project_path, storage_path
FROM session_sources WHERE project_path <> '' AND storage_path <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byFolder := map[string]map[string]struct{}{}
	for rows.Next() {
		var project, storage string
		if err := rows.Scan(&project, &storage); err != nil {
			return nil, err
		}
		folder := filepath.Dir(storage)
		if byFolder[folder] == nil {
			byFolder[folder] = map[string]struct{}{}
		}
		byFolder[folder][project] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, projects := range byFolder {
		// A folder dedicated to one working directory holds the path before
		// the move and the path after it, and little else. Codex keeps every
		// session of a day in one folder instead, so a crowded folder says
		// nothing about where a project went.
		if len(projects) < 2 || len(projects) > maxColocatedDirectories {
			continue
		}
		for project := range projects {
			for other := range projects {
				if other != project {
					out[project] = append(out[project], other)
				}
			}
		}
	}
	for project := range out {
		sort.Strings(out[project])
		out[project] = dedupeStrings(out[project])
	}
	return out, nil
}

// directoryCandidates proposes where a missing directory moved to. Only two
// kinds of evidence qualify, both checkable: another directory whose sessions
// share a provider storage folder with this one, and an existing indexed
// directory that ends in the same path segments.
func directoryCandidates(missing string, live []string, colocated []string) []DirectoryCandidate {
	var out []DirectoryCandidate
	seen := map[string]bool{}
	add := func(path, why string, minShared int) {
		if path == "" || path == missing || seen[path] || !dirExists(path) {
			return
		}
		// A project that moved keeps its own name. Without shared trailing
		// segments there is nothing tying the two directories together, and a
		// proposal would be a guess.
		if sharedTrailingSegments(missing, path) < minShared {
			return
		}
		alias, ok := aliasBetween(missing, path)
		if !ok {
			return
		}
		seen[path] = true
		out = append(out, DirectoryCandidate{Path: path, Alias: alias, Why: why})
	}
	for _, path := range colocated {
		add(path, "shares a provider storage folder with this directory", 1)
	}
	best := 0
	var bestPaths []string
	for _, path := range live {
		n := sharedTrailingSegments(missing, path)
		if n < minTrailingSegments || n < best {
			continue
		}
		if n > best {
			best, bestPaths = n, nil
		}
		bestPaths = append(bestPaths, path)
	}
	for _, path := range bestPaths {
		add(path, fmt.Sprintf("ends in the same %d path segments", best), minTrailingSegments)
	}
	return out
}

// aliasBetween reduces a pair of directories to the shortest alias that maps
// one onto the other, so renaming Projects/fit to Work/fit/projects is stated
// once instead of once per project below it.
func aliasBetween(missing, candidate string) (config.PathAlias, bool) {
	shared := sharedTrailingSegments(missing, candidate)
	from := trimTrailingSegments(missing, shared)
	to := trimTrailingSegments(candidate, shared)
	if from == "" || to == "" || from == to {
		return config.PathAlias{}, false
	}
	return config.PathAlias{From: from, To: to}, true
}

func sharedTrailingSegments(a, b string) int {
	as, bs := pathSegments(a), pathSegments(b)
	n := 0
	for n < len(as) && n < len(bs) && as[len(as)-1-n] == bs[len(bs)-1-n] {
		n++
	}
	// A whole path is never "shared trailing segments"; that would leave an
	// empty prefix to alias.
	if n == len(as) || n == len(bs) {
		n--
	}
	return max(n, 0)
}

func trimTrailingSegments(path string, n int) string {
	for ; n > 0; n-- {
		path = filepath.Dir(path)
	}
	return path
}

func pathSegments(path string) []string {
	trimmed := strings.Trim(filepath.ToSlash(path), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func dedupeStrings(in []string) []string {
	out := in[:0]
	var last string
	for i, s := range in {
		if i > 0 && s == last {
			continue
		}
		last = s
		out = append(out, s)
	}
	return out
}

func dirExists(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// SetPathAliases replaces the aliases this store applies when it projects
// sessions from their sources.
func (s *Store) SetPathAliases(aliases []config.PathAlias) {
	s.pathAliases = normalizeAliases(aliases)
}

// PathAliases reports the aliases this store is applying.
func (s *Store) PathAliases() []config.PathAlias { return s.pathAliases }

func normalizeAliases(aliases []config.PathAlias) []config.PathAlias {
	out := make([]config.PathAlias, 0, len(aliases))
	for _, alias := range aliases {
		from := strings.TrimRight(util.NormalizeProjectPath(alias.From), "/")
		to := strings.TrimRight(util.NormalizeProjectPath(alias.To), "/")
		if from == "" || to == "" || from == to {
			continue
		}
		out = append(out, config.PathAlias{From: from, To: to})
	}
	return out
}

// applyPathAliases rewrites the directory of already projected sessions. The
// evidence in session_sources keeps the directory the agent actually recorded,
// so removing an alias restores the original without re-reading any file.
func applyPathAliases(tx *sql.Tx, providerID string, aliases []config.PathAlias) error {
	for _, alias := range aliases {
		prefix := alias.From + string(filepath.Separator)
		if _, err := tx.Exec(`UPDATE sessions
SET project_path = ? || substr(project_path, ?)
WHERE provider = ? AND project_path LIKE ? ESCAPE '\'`,
			alias.To, len(alias.From)+1, providerID, util.EscapeLike(prefix)+"%"); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET project_path = ?
WHERE provider = ? AND project_path = ?`, alias.To, providerID, alias.From); err != nil {
			return err
		}
	}
	return nil
}

// ReprojectSessions rebuilds every session row from its recorded source and
// re-applies the current aliases. Changing an alias is a decision about
// presentation, not about what the agents wrote, so it must not require
// re-reading a single provider file.
func (s *Store) ReprojectSessions() error {
	rows, err := s.db.Query(`SELECT DISTINCT provider FROM session_sources`)
	if err != nil {
		return err
	}
	var providers []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		providers = append(providers, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, providerID := range providers {
		if err := rebuildProviderSessions(tx, providerID); err != nil {
			return err
		}
		if err := applyPathAliases(tx, providerID, s.pathAliases); err != nil {
			return err
		}
	}
	return tx.Commit()
}
