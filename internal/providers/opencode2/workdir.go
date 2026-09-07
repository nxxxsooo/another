package opencode2

import (
	"os"
	"path/filepath"
)

// withSessionDirectory runs a lifecycle call against a session whose recorded
// working directory may be gone.
//
// OpenCode 2's server opens a session's directory before it will rename or
// delete it, and `opencode2 api` exits 0 on the resulting HTTP 500. A session
// left behind by a merged worktree or a temporary checkout was therefore
// impossible to remove, and another could only report the refusal.
//
// The directory is restored for the length of the call and then withdrawn
// again: only the levels another created, only while they are still empty, and
// deepest first. A directory that already existed is never touched, and a
// directory the operation filled is left alone.
func (p *Provider) withSessionDirectory(sessionID string, fn func() error) error {
	created := p.restoreSessionDirectory(sessionID)
	defer withdrawDirectories(created)
	return fn()
}

// restoreSessionDirectory recreates a session's missing directory and reports
// the levels it had to create, deepest first. It reports nothing when the
// directory is already there, unknown, or unusable, because in those cases the
// call has to stand on its own.
func (p *Provider) restoreSessionDirectory(sessionID string) []string {
	dir, err := p.storedDirectory(sessionID)
	if err != nil || dir == "" || !filepath.IsAbs(dir) {
		return nil
	}
	var created []string
	for path := dir; path != "" && path != string(filepath.Separator); path = filepath.Dir(path) {
		if _, err := os.Stat(path); err == nil {
			break
		}
		created = append(created, path)
	}
	if len(created) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Withdraw whatever MkdirAll managed to create before it failed, so a
		// refused call never leaves a directory tree behind.
		withdrawDirectories(created)
		return nil
	}
	return created
}

// withdrawDirectories removes the levels another created, deepest first, and
// stops at the first one it cannot remove. os.Remove refuses a non-empty
// directory, which is exactly the guard this needs: anything the operation
// wrote stays.
func withdrawDirectories(created []string) {
	for _, path := range created {
		if err := os.Remove(path); err != nil {
			return
		}
	}
}
