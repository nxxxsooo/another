package codex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"

	"github.com/nxxxsooo/another/internal/util"
)

// projectLabels is a display-only projection of native ChatGPT project roots.
// An empty value records an ambiguous or unusable authoritative association.
type projectLabels struct {
	backing string
	roots   map[string]string
}

func newProjectLabels(home string) projectLabels {
	return projectLabels{backing: labelPath(filepath.Join(home, ".chatgpt-projects")), roots: make(map[string]string)}
}

func labelPath(path string) string {
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(path, `\\?\UNC\`) {
			path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
		} else {
			path = strings.TrimPrefix(path, `\\?\`)
		}
	}
	return util.NormalizeProjectPath(path)
}

func labelPathKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func labelRelative(root, path string) (string, bool) {
	rel, err := filepath.Rel(labelPathKey(root), labelPathKey(path))
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	// Preserve native spelling of child directories on case-insensitive Windows.
	if runtime.GOOS == "windows" && rel != "." {
		parts := strings.Split(rel, string(filepath.Separator))
		original := strings.Split(path, string(filepath.Separator))
		rel = filepath.Join(original[len(original)-len(parts):]...)
	}
	return rel, true
}

func validProjectName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsFunc(name, unicode.IsControl) {
		return ""
	}
	return name
}

func (p projectLabels) root(path string) string {
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	path = labelPath(path)
	rel, ok := labelRelative(p.backing, path)
	if !ok || rel == "." {
		return ""
	}
	first := strings.Split(rel, string(filepath.Separator))[0]
	if !strings.HasPrefix(first, "g-p-") || len(first) == len("g-p-") {
		return ""
	}
	return labelPathKey(path)
}

func addProjectRoot(roots map[string]string, root, name string) {
	if previous, ok := roots[root]; ok && previous != name {
		roots[root] = ""
	} else if !ok {
		roots[root] = name
	}
}

func (p projectLabels) loadDesktop(data []byte) {
	var state struct {
		Projects map[string]json.RawMessage `json:"local-projects"`
	}
	if json.Unmarshal(data, &state) != nil {
		return
	}
	for _, raw := range state.Projects {
		var project struct {
			ID    string   `json:"id"`
			Name  string   `json:"name"`
			Roots []string `json:"rootPaths"`
		}
		if json.Unmarshal(raw, &project) != nil || project.ID == "" {
			continue
		}
		for _, path := range project.Roots {
			if root := p.root(path); root != "" {
				addProjectRoot(p.roots, root, validProjectName(project.Name))
			}
		}
	}
}

func (p projectLabels) loadNative(ctx context.Context, db *sql.DB) error {
	// Optional schemas vary across native versions. Query only supported columns;
	// actual read failures still surface through the native metadata error path.
	for table, columns := range map[string][]string{"projects": {"id", "name"}, "project_roots": {"project_id", "path"}} {
		for _, column := range columns {
			var count int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				return nil
			}
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT p.name, r.path FROM projects p JOIN project_roots r ON r.project_id = p.id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	native := make(map[string]string)
	for rows.Next() {
		var name, path sql.NullString
		if err := rows.Scan(&name, &path); err != nil {
			return err
		}
		if root := p.root(path.String); root != "" {
			addProjectRoot(native, root, validProjectName(name.String))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for root, name := range native {
		p.roots[root] = name
	}
	return nil
}

func (p projectLabels) label(path string) string {
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	path = labelPath(path)
	best, name, relative := "", "", ""
	for root, candidate := range p.roots {
		if rel, ok := labelRelative(root, path); ok && len(root) > len(best) {
			best, name, relative = root, candidate, rel
		}
	}
	if name == "" || relative == "." {
		return name
	}
	return name + string(filepath.Separator) + relative
}

// Hash only the effective applicable catalog, once loaded per discovery, never
// the whole database or desktop state. This also covers metadata-only removal.
func (p projectLabels) revision() uint64 {
	if len(p.roots) == 0 {
		return 0
	}
	keys := make([]string, 0, len(p.roots))
	for root := range p.roots {
		keys = append(keys, root)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, root := range keys {
		_, _ = h.Write([]byte(root + "\x00" + p.roots[root] + "\x00"))
	}
	return binary.LittleEndian.Uint64(h.Sum(nil)[:8])
}
