package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// guiTitles is Codex Desktop's own session-title catalog. A title here is also
// authoritative evidence that a rollout is user-visible: desktop-launched
// threads can carry subagent-shaped source metadata even while appearing in the
// sidebar.
type guiTitles struct {
	names           map[string]string
	nativeRevisions map[string]uint64
	mtime           int64
	size            int64
}

func (p *Provider) loadGUITitles(ctx context.Context) (guiTitles, error) {
	root := filepath.Dir(p.sessionsRoot)
	out := guiTitles{names: make(map[string]string), nativeRevisions: make(map[string]uint64)}
	if err := ctx.Err(); err != nil {
		return out, err
	}

	// Older Desktop builds store generated descriptions in persisted Electron
	// state. Keep this as the broad fallback.
	global := filepath.Join(root, ".codex-global-state.json")
	if data, err := os.ReadFile(global); err == nil {
		var state struct {
			Electron struct {
				Titles map[string]string `json:"thread-descriptions-v1"`
			} `json:"electron-persisted-atom-state"`
		}
		if json.Unmarshal(data, &state) == nil {
			for id, title := range state.Electron.Titles {
				if id != "" && title != "" {
					out.names[id] = title
				}
			}
		}
		out.noteFile(global)
	}

	// Older CLI/Desktop versions append names here. Native thread metadata below
	// takes precedence: current clients no longer use this as the title authority.
	indexPath := filepath.Join(root, "session_index.jsonl")
	if f, err := os.Open(indexPath); err == nil {
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var row struct {
				ID   string `json:"id"`
				Name string `json:"thread_name"`
			}
			if json.Unmarshal(scanner.Bytes(), &row) == nil && row.ID != "" && row.Name != "" {
				out.names[row.ID] = row.Name
			}
		}
		_ = f.Close()
		out.noteFile(indexPath)
	}
	if err := p.loadNativeNames(ctx, &out); err != nil {
		return out, fmt.Errorf("codex: read native thread names: %w", err)
	}
	return out, nil
}

// loadNativeNames reads the same name returned by thread/read. The legacy
// title/preview columns describe the initial prompt, not the current name.
// Fingerprint each native name, rather than the whole database/WAL: an unrelated
// turn updates the database too, but must not re-index every Codex transcript.
func (p *Provider) loadNativeNames(ctx context.Context, titles *guiTitles) error {
	path, err := p.resolveStateDB()
	if err != nil || path == "" {
		return err
	}
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var hasName int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('threads') WHERE name = 'name'`).Scan(&hasName); err != nil {
		return err
	}
	if hasName == 0 {
		return nil // A pre-name database still uses the legacy catalogs above.
	}
	rows, err := db.QueryContext(ctx, `SELECT id, name, rollout_path FROM threads`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, rolloutPath string
		var name sql.NullString
		if err := rows.Scan(&id, &name, &rolloutPath); err != nil {
			return err
		}
		delete(titles.names, id)
		if name.Valid && strings.TrimSpace(name.String) != "" {
			titles.names[id] = name.String
		}
		revision := sha256.Sum256([]byte("codex-native-name\x00" + id + "\x00" + name.String))
		titles.nativeRevisions[filepath.Clean(rolloutPath)] = binary.LittleEndian.Uint64(revision[:8])
	}
	return rows.Err()
}

func (g *guiTitles) noteFile(path string) {
	if st, err := os.Stat(path); err == nil {
		if n := st.ModTime().UnixNano(); n > g.mtime {
			g.mtime = n
		}
		g.size += st.Size()
	}
}

func (g guiTitles) fingerprint(st os.FileInfo, path string) (int64, int64) {
	mtime := st.ModTime().UnixNano()
	if revision, ok := g.nativeRevisions[filepath.Clean(path)]; ok {
		// SourceSize is an opaque index fingerprint, not a displayed byte count.
		return mtime, int64((uint64(st.Size()) ^ revision) & ((1 << 63) - 1))
	}
	if g.mtime > mtime {
		mtime = g.mtime
	}
	return mtime, st.Size() + g.size
}
