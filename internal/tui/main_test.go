package tui

import (
	"os"
	"testing"
)

// TestMain points the action journal at a throwaway cache for the whole test
// binary. Recording is best-effort and keyed off the cache directory, so a test
// that exercises a command would otherwise write real history into the
// developer's own ~/.cache/another/history.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "another-tui-test-cache")
	if err == nil {
		_ = os.Setenv("XDG_CACHE_HOME", dir)
	} else {
		dir = ""
	}
	code := m.Run()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}
