package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelocateCommandSurface(t *testing.T) {
	root := (&App{}).Root()
	cmd, _, err := root.Find([]string{"relocate"})
	if err != nil || cmd == root {
		t.Fatalf("missing relocate command: %v", err)
	}
	for _, flag := range []string{"to-dir", "from", "move", "dry-run", "yes", "refresh"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing relocate --%s", flag)
		}
	}
	// Fork is the default: the source session survives unless the person asks
	// for a move.
	if flag := cmd.Flags().Lookup("move"); flag == nil || flag.DefValue != "false" {
		t.Fatalf("relocate --move default = %#v", flag)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Fatal("relocate accepted two session IDs")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Fatal("relocate accepted no session ID")
	}
}

// The target directory is validated before anything reads the index, so a typo
// fails immediately instead of producing a session pointing at nothing.
func TestRelocateRejectsBadTargetsBeforeUsingApp(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no target", []string{"relocate", "session"}, "--to-dir is required"},
		{"missing directory", []string{"relocate", "session", "--to-dir", filepath.Join(t.TempDir(), "nope")}, "does not exist"},
		{"a file is not a directory", []string{"relocate", "session", "--to-dir", file}, "is not a directory"},
		{"empty target", []string{"relocate", "session", "--to-dir", "  "}, "--to-dir is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := (&App{}).Root()
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
