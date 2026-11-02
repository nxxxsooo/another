package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/pi"
	"github.com/nxxxsooo/another/internal/titler"
)

func TestRenameCommandSurface(t *testing.T) {
	root := (&App{}).Root()
	cmd, _, err := root.Find([]string{"rename"})
	if err != nil || cmd == root {
		t.Fatalf("missing rename command: %v", err)
	}
	for _, flag := range []string{"title", "auto", "from", "allow-current", "dry-run", "refresh", "if-stale", "replace-native", "skip-conforming", "hook-stdin"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing rename --%s", flag)
		}
	}
	// Hooks installed by earlier releases still pass --skip-conforming, so it
	// must parse; it is not offered to anyone new.
	if !cmd.Flags().Lookup("skip-conforming").Hidden {
		t.Error("rename --skip-conforming should be hidden")
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

// Staleness is a property of an automatic title; the flags are refused
// before anything reads the index when there is no automatic title to judge.
func TestRenameStaleFlagsNeedTheirPrerequisites(t *testing.T) {
	for args, want := range map[string][]string{
		"--if-stale needs --auto":           {"rename", "session", "--title", "x", "--if-stale"},
		"--replace-native needs --if-stale": {"rename", "session", "--auto", "--replace-native"},
	} {
		root := (&App{}).Root()
		root.SetArgs(want)
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), args) {
			t.Fatalf("%v: error = %v, want %q", want, err, args)
		}
	}
}

// The dynamic title from the hook's point of view: one call after every turn,
// and the title moves only when the session did.
func TestRenameIfStaleFollowsTheSession(t *testing.T) {
	alpha := newFake(t, "alpha")
	day := func(d, hour int) time.Time { return time.Date(2026, 9, d, hour-8, 0, 0, 0, time.UTC) } // Shanghai hour
	conv := model.Conversation{
		ID: "s", ProjectPath: t.TempDir(), CreatedAt: day(1, 9), UpdatedAt: day(1, 9),
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "the stripe webhook retries forever", Timestamp: day(1, 9)},
			{Role: model.RoleAssistant, Content: "make the handler idempotent", Timestamp: day(1, 9)},
		},
	}
	alpha.seed(t, conv)
	app := newTestApp(t, alpha)
	if err := config.SaveSettings(config.Settings{TitleModel: &config.TitleModel{Provider: "pi", Language: "zh"}}); err != nil {
		t.Fatal(err)
	}

	var requests []titler.Request
	answers := []string{}
	old := suggestRetrying
	suggestRetrying = func(_ context.Context, _ titler.Config, req titler.Request) (string, error) {
		requests = append(requests, req)
		if len(answers) == 0 {
			t.Fatalf("unexpected title call %d", len(requests))
		}
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}
	t.Cleanup(func() { suggestRetrying = old })

	hook := func() string {
		t.Helper()
		return mustRun(t, app, "rename", "s", "--from", "alpha", "--auto", "--if-stale", "--refresh")
	}
	talk := func(turns int, at time.Time) {
		t.Helper()
		current, err := alpha.read("s")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < turns; i++ {
			current.Messages = append(current.Messages,
				model.Message{Role: model.RoleUser, Content: "next step", Timestamp: at},
				model.Message{Role: model.RoleAssistant, Content: "done", Timestamp: at})
		}
		current.UpdatedAt = at
		alpha.save(t, current)
	}

	// First turn: an untitled session is named, dated by its activity.
	answers = []string{"0901｜修复｜Stripe重试"}
	wantContains(t, hook(), "Renamed s to 0901｜修复｜Stripe重试")

	// A turn that changed nothing costs nothing.
	wantContains(t, hook(), `keeps "0901｜修复｜Stripe重试" (fresh)`)

	// Three more turns on the same day: the model is asked, keeps the topic,
	// and nothing is written — but the turn count restarts from here.
	talk(3, day(1, 15))
	answers = []string{"0901｜修复｜Stripe重试"}
	wantContains(t, hook(), "still fits")
	if !requests[1].Refresh {
		t.Fatal("drift check was not asked as a refresh")
	}
	wantContains(t, hook(), "(fresh)")

	// The next morning: the date follows the activity even when the topic is kept.
	talk(1, day(2, 10))
	answers = []string{""} // what Suggest returns when the model answers KEEP
	wantContains(t, hook(), "Renamed s to 0902｜修复｜Stripe重试")
	if got := titler.MMDD(requests[2].Date); got != "0902" {
		t.Fatalf("refresh asked for date %s, want 0902", got)
	}

	// Three turns later the topic has moved, and the title follows it.
	talk(3, day(2, 16))
	answers = []string{"0901｜功能｜动态会话标题"}
	wantContains(t, hook(), "Renamed s to 0902｜功能｜动态会话标题")

	// A person renames it. No amount of activity replaces that.
	mustRun(t, app, "rename", "s", "--from", "alpha", "--title", "my own name")
	talk(5, day(4, 10))
	wantContains(t, hook(), "(set by hand)")
	out := mustRun(t, app, "rename", "s", "--from", "alpha", "--auto", "--if-stale", "--replace-native", "--refresh")
	wantContains(t, out, "(set by hand)")
	if len(requests) != 4 {
		t.Fatalf("title calls = %d, want 4", len(requests))
	}
}

// Pi never names a session itself, so its extension passes no
// --replace-native. An unnamed Pi session still shows its first prompt as a
// title; that fallback must be named rather than kept as a person's choice,
// while a name set with /name stays.
func TestRenameIfStaleNamesAnUnnamedPiSession(t *testing.T) {
	t.Setenv(pi.AgentDirEnv, t.TempDir())
	p := pi.New()
	app := newTestApp(t, p)
	conv := sampleConversation("source", t.TempDir(), "")
	res, err := p.Write(context.Background(), &conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// Write always names what it writes; a session Pi starts has no name.
	data, err := os.ReadFile(res.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &row) == nil && row.Type == "session_info" {
			continue
		}
		rows = append(rows, line)
	}
	if err := os.WriteFile(res.StoragePath, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSettings(config.Settings{TitleModel: &config.TitleModel{Provider: "pi", Language: "zh"}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	old := suggestRetrying
	suggestRetrying = func(context.Context, titler.Config, titler.Request) (string, error) {
		calls++
		return "0901｜修复｜Stripe重试", nil
	}
	t.Cleanup(func() { suggestRetrying = old })

	hook := func() string {
		t.Helper()
		return mustRun(t, app, "rename", res.SessionID, "--from", "pi", "--auto", "--if-stale", "--allow-current", "--refresh")
	}
	wantContains(t, hook(), "Renamed "+res.SessionID+" to ", "｜修复｜Stripe重试")

	ref := provider.SessionRef{ID: res.SessionID, StoragePath: res.StoragePath}
	if err := p.RenameSession(context.Background(), ref, "my own name"); err != nil {
		t.Fatal(err)
	}
	wantContains(t, hook(), `keeps "my own name" (set by hand)`)
	if calls != 1 {
		t.Fatalf("title calls = %d, want 1", calls)
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
