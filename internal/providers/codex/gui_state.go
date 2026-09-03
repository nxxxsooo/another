package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// guiTitles is Codex Desktop's own session-title catalog. A title here is also
// authoritative evidence that a rollout is user-visible: desktop-launched
// threads can carry subagent-shaped source metadata even while appearing in the
// sidebar.
type guiTitles struct {
	names map[string]string
	mtime int64
	size  int64
}

func (p *Provider) loadGUITitles() guiTitles {
	root := filepath.Dir(p.sessionsRoot)
	out := guiTitles{names: make(map[string]string)}

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

	// Current Desktop builds append explicit user-facing names here. Last entry
	// wins because renames append a new row. These short names are exactly what
	// the sidebar shows, so they override generated descriptions above.
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
	return out
}

func (g *guiTitles) noteFile(path string) {
	if st, err := os.Stat(path); err == nil {
		if n := st.ModTime().UnixNano(); n > g.mtime {
			g.mtime = n
		}
		g.size += st.Size()
	}
}

func (p *Provider) appendGUITitle(sessionID, title string) error {
	title = strings.TrimSpace(title)
	if sessionID == "" || title == "" {
		return fmt.Errorf("codex: session id and title are required")
	}
	path := filepath.Join(filepath.Dir(p.sessionsRoot), "session_index.jsonl")
	row, err := json.Marshal(map[string]any{
		"id": sessionID, "thread_name": title, "updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(row, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (g guiTitles) fingerprint(st os.FileInfo) (int64, int64) {
	mtime := st.ModTime().UnixNano()
	if g.mtime > mtime {
		mtime = g.mtime
	}
	return mtime, st.Size() + g.size
}
