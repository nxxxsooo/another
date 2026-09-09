package codex

import (
	"os"
	"path/filepath"
)

// desktopStateDir is where Codex Desktop keeps its Electron state and the
// singleton lock beside it.
func desktopStateDir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, "Library", "Application Support", "Codex"), true
}
