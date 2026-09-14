//go:build windows

package opencodeunified

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnifiedProviderFindsWindowsNativeStores(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB_PATH", "")
	t.Setenv("OPENCODE2_DB_PATH", "")
	if err := os.MkdirAll(filepath.Join(root, "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	paths := New().DefaultPaths()
	for i, name := range []string{"opencode.db", "opencode2.db"} {
		want := filepath.Join(root, "opencode", name)
		if len(paths) <= i || paths[i].Path != want {
			t.Fatalf("native store %s missing: %+v", want, paths)
		}
	}
}
