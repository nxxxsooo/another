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
	for _, flag := range []string{"title", "auto", "from", "allow-current", "dry-run", "refresh", "skip-conforming", "hook-stdin"} {
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
	// With --hook-stdin the id arrives on stdin, so an empty argument list is
	// valid at this layer; the payload is judged once it has been read.
	if err := cmd.Args(cmd, nil); err != nil {
		t.Fatalf("rename refused zero args: %v", err)
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

// The agy Stop hook speaks for a session it cannot name on the command line:
// the id arrives inside the hook payload on stdin. --hook-stdin is that
// contract — one flag, no interpreter, no second policy.
func TestHookPayloadExtractsTheSessionID(t *testing.T) {
	// The payload agy 1.3.3 actually sends, captured live and reduced.
	agy := []byte(`{"conversationId":"f9d88aaf-71b0-4ac5-8e15-b1809bf51ced","fullyIdle":true,
		"transcriptPath":"/tmp/t.jsonl","workspacePaths":["/tmp"],"terminationReason":"NO_TOOL_CALL"}`)
	id, idle, err := parseHookPayload(agy)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if id != "f9d88aaf-71b0-4ac5-8e15-b1809bf51ced" {
		t.Fatalf("id = %q", id)
	}
	if !idle {
		t.Fatal("fullyIdle true was read as false")
	}

	// claude-code's hook payload spells the key differently; the extractor
	// accepts both so one flag can serve every hook-shaped caller.
	id, _, err = parseHookPayload([]byte(`{"session_id":"abc123"}`))
	if err != nil || id != "abc123" {
		t.Fatalf("id, err = %q, %v", id, err)
	}

	// A queued continuation means the agent is still working; renaming now
	// races the next turn. The caller decides what to do with that.
	_, idle, err = parseHookPayload([]byte(`{"conversation_id":"x","fullyIdle":false}`))
	if err != nil || idle {
		t.Fatalf("idle, err = %v, %v", idle, err)
	}
}

func TestHookPayloadRefusesWhatItCannotName(t *testing.T) {
	for _, payload := range []string{
		`{}`,
		`not json`,
		``,
	} {
		if _, _, err := parseHookPayload([]byte(payload)); err == nil {
			t.Errorf("payload %q parsed without error", payload)
		}
	}
}

// A hook that passes the id twice has two names for one session; refuse
// instead of silently preferring one.
func TestRenameRefusesBothIDSources(t *testing.T) {
	root := (&App{}).Root()
	root.SetArgs([]string{"rename", "session", "--hook-stdin", "--title", "x"})
	root.SetIn(strings.NewReader(`{"session_id":"abc"}`))
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "either") {
		t.Fatalf("error = %v, want the one-source refusal", err)
	}
}
