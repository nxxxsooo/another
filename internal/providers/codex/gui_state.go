package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nxxxsooo/another/internal/provider"
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

// desktopTitleKey is the map Codex Desktop's sidebar actually reads. A rename
// that only reaches the CLI's thread store leaves the sidebar on the old name,
// which is what the person is looking at.
const desktopTitleKey = "thread-descriptions-v1"

// desktopStateKey is the object that map lives in, inside the Electron state.
const desktopStateKey = "electron-persisted-atom-state"

// writeDesktopTitle puts the new name where Desktop reads it.
//
// The file is Desktop's whole persisted state, and Desktop rewrites all of it
// from memory while it runs, so writing underneath a running app either loses
// the rename or loses whatever the app has not flushed yet. When Desktop is
// running this reports provider.ErrPartial instead: the CLI's own store has
// already been renamed, and the sidebar catches up on the next restart.
func (p *Provider) writeDesktopTitle(sessionID, title string) error {
	path := filepath.Join(filepath.Dir(p.sessionsRoot), ".codex-global-state.json")
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		// No Desktop on this machine; the CLI store is the only surface.
		return nil
	}
	if err != nil {
		return err
	}
	if desktopRunning() {
		return fmt.Errorf("%w: Codex Desktop is running, so its sidebar keeps the old name until it restarts",
			provider.ErrPartial)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// UseNumber keeps every number byte-for-byte. Decoding this file through
	// float64 would rewrite the millisecond timestamps in it as exponents.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var state map[string]any
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("codex: read desktop state: %w", err)
	}
	persisted, ok := state[desktopStateKey].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: Codex Desktop state has no %s object to rename in",
			provider.ErrPartial, desktopStateKey)
	}
	titles, ok := persisted[desktopTitleKey].(map[string]any)
	if !ok {
		// Desktop writes this map the first time it names a thread. Creating it
		// here would be inventing a shape this build has never seen.
		return fmt.Errorf("%w: Codex Desktop state has no %s map to rename in",
			provider.ErrPartial, desktopTitleKey)
	}
	if current, _ := titles[sessionID].(string); current == title {
		return nil
	}
	titles[sessionID] = title

	// Desktop does not escape HTML in its own state, and json.Marshal does.
	// Rewriting every < and & as an escape would leave a file that parses the
	// same and looks nothing like the one Desktop wrote.
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return err
	}
	return writeFileAtomic(path, bytes.TrimRight(out.Bytes(), "\n"), st.Mode().Perm())
}

// writeFileAtomic replaces a file by rename, so a crash mid-write cannot leave
// Desktop with half of its own state.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// desktopRunning reports whether Codex Desktop holds its singleton lock. The
// lock is a symlink Chromium points at "<host>-<pid>" and removes on a clean
// exit; the pid is checked because a crash leaves the link behind.
func desktopRunning() bool {
	dir := os.Getenv("CODEX_DESKTOP_STATE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, "Library", "Application Support", "Codex")
	}
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	idx := strings.LastIndex(target, "-")
	if idx < 0 {
		return false
	}
	pid, err := strconv.Atoi(target[idx+1:])
	if err != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 asks whether the process exists. Not being allowed to signal it
	// is an answer too: Desktop is running, it just is not ours to touch.
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (g guiTitles) fingerprint(st os.FileInfo) (int64, int64) {
	mtime := st.ModTime().UnixNano()
	if g.mtime > mtime {
		mtime = g.mtime
	}
	return mtime, st.Size() + g.size
}
