package cli

import (
	"strings"
	"testing"
)

func TestRenameCommandSurface(t *testing.T) {
	root := (&App{}).Root()
	cmd, _, err := root.Find([]string{"rename"})
	if err != nil || cmd == root {
		t.Fatalf("missing rename command: %v", err)
	}
	for _, flag := range []string{"title", "auto", "from", "allow-current", "dry-run", "refresh", "skip-conforming"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing rename --%s", flag)
		}
	}
	// Renaming the running session stays refused unless a caller that knows it
	// is a SessionEnd hook says otherwise.
	if flag := cmd.Flags().Lookup("allow-current"); flag == nil || flag.DefValue != "false" {
		t.Fatalf("rename --allow-current default = %#v", flag)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Fatal("rename accepted two session IDs")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Fatal("rename accepted no session ID")
	}
}

// The title source is settled before anything reads the index, so a hook that
// passes neither flag fails immediately rather than after a provider scan.
func TestRenameRequiresExactlyOneTitleSource(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"neither", []string{"rename", "session"}},
		{"both", []string{"rename", "session", "--title", "x", "--auto"}},
		// A title of only spaces is not a title. Without --auto there is
		// nothing left to rename to, so this is the "neither" case in disguise.
		{"blank title", []string{"rename", "session", "--title", "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := (&App{}).Root()
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "exactly one of --title or --auto") {
				t.Fatalf("error = %v, want the exactly-one refusal", err)
			}
		})
	}
}
