package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/pi"
)

func TestRelocateCommandSurface(t *testing.T) {
	root := (&App{}).Root()
	cmd, _, err := root.Find([]string{"relocate"})
	if err != nil || cmd == root {
		t.Fatalf("missing relocate command: %v", err)
	}
	for _, flag := range []string{"to-dir", "from", "move", "fork", "dry-run", "yes", "refresh", "create"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing relocate --%s", flag)
		}
	}
	// Several sessions can go to one directory in one call, the way a batch
	// rename takes a marked set.
	if err := cmd.Args(cmd, []string{"a", "b"}); err != nil {
		t.Fatalf("relocate refused two session IDs: %v", err)
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Fatal("relocate accepted no session ID")
	}
}

// Exercise the command against Pi's real session files: a wrong default must
// fail on the source's survival and the destination's identity, not just help.
func TestRelocateNativePiModes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		move  bool
	}{
		{"default moves", nil, true},
		{"explicit move", []string{"--move"}, true},
		{"explicit fork", []string{"--fork"}, false},
		{"legacy move false forks", []string{"--move=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(pi.AgentDirEnv, t.TempDir())
			p := pi.New()
			app := newTestApp(t, p)
			sourceDir := t.TempDir()
			target, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			conv := sampleConversation("source", sourceDir, "Native session")
			source, err := p.Write(context.Background(), &conv, provider.WriteOpts{ProjectPath: sourceDir})
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(source.StoragePath)
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{"relocate", source.SessionID, "--to-dir", target, "--yes"}, tc.flags...)
			out := mustRun(t, app, args...)
			if tc.move {
				wantContains(t, out, "Moved "+source.SessionID+" to "+target)
				if _, err := os.Stat(source.StoragePath); !os.IsNotExist(err) {
					t.Fatalf("move left the original: %v", err)
				}
			} else {
				wantContains(t, out, "Forked "+source.SessionID, "Source session "+source.SessionID+" is unchanged")
				data, err := os.ReadFile(source.StoragePath)
				if err != nil || string(data) != string(original) {
					t.Fatalf("fork changed the original: %v", err)
				}
			}
			sessions, err := p.Discover(context.Background(), provider.DiscoverOpts{})
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 2
			if tc.move {
				wantCount = 1
			}
			if len(sessions) != wantCount {
				t.Fatalf("sessions = %d, want %d", len(sessions), wantCount)
			}
			var landed bool
			for _, sm := range sessions {
				if sm.ProjectPath != target {
					continue
				}
				landed = true
				if (sm.ID == source.SessionID) != tc.move {
					t.Fatalf("destination identity = %s, source = %s, move = %v", sm.ID, source.SessionID, tc.move)
				}
				data, err := os.ReadFile(sm.StoragePath)
				if err != nil {
					t.Fatal(err)
				}
				if strings.SplitN(string(data), "\n", 2)[1] != strings.SplitN(string(original), "\n", 2)[1] {
					t.Fatal("relocation rewrote the native records")
				}
			}
			if !landed {
				t.Fatal("no session landed in the target directory")
			}
		})
	}
}

func TestRelocateRejectsConflictingModesBeforeCreatingTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "fresh")
	root := (&App{}).Root()
	root.SetArgs([]string{"relocate", "session", "--to-dir", target, "--create", "--fork", "--move", "--yes"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "fork") || !strings.Contains(err.Error(), "move") {
		t.Fatalf("conflicting modes error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("conflicting modes created the target: %v", err)
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
		{"missing directory", []string{"relocate", "session", "--to-dir", filepath.Join(t.TempDir(), "nope")}, "(pass --create to make it)"},
		{"a file is not created over", []string{"relocate", "session", "--to-dir", file, "--create"}, "is not a directory"},
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
