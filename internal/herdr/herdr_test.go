package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fake struct {
	responses map[string]string
	fail      map[string]error
	calls     [][]string
}

func fixture() *fake {
	return &fake{responses: map[string]string{
		"hc server status --json": `{"pid":42}`,
		"hc ls --json":            `{"terminals":[{"address":"t1","agent":"opencode","state":"live"}]}`,
		"hc get t1 --json":        `{"address":"t1","state":"live","status":{"app":"opencode"},"records":{"ses_exact":{"app":"opencode","state":"working"},"ses_internal":{"app":"opencode","state":"idle"}}}`,
		"herdr-app ls":            `{"spaces":[{"id":"remote","active":true,"machine":"vps"},{"id":"local","machine":null}]}`,
	}, fail: map[string]error{}}
}

func (f *fake) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		return nil, errors.New("unbounded command")
	}
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, " ")
	if err := f.fail[key]; err != nil {
		return nil, err
	}
	if out, ok := f.responses[key]; ok {
		return []byte(out), nil
	}
	if len(args) == 3 && name == "herdr-app" && args[0] == "do" {
		return []byte(`{"terminal":"t2","tab":"new"}`), nil
	}
	return nil, fmt.Errorf("unexpected command: %s", key)
}

func (f *fake) actions() [][]string {
	var actions [][]string
	for _, call := range f.calls {
		if len(call) > 1 && call[0] == "herdr-app" && call[1] == "do" {
			actions = append(actions, call)
		}
	}
	return actions
}

func request(t *testing.T) Request {
	t.Helper()
	return Request{Provider: "opencode", ID: "ses_exact", Name: "A 'quoted' title", Folder: filepath.Join(t.TempDir(), "project [one]"), Resume: func() ([]string, error) { return []string{"opencode", "--session", "ses_exact"}, nil }}
}

func TestFocusExactNativeIdentityAndInternalSessions(t *testing.T) {
	for _, id := range []string{"ses_exact", "ses_internal"} {
		t.Run(id, func(t *testing.T) {
			f := fixture()
			req := request(t)
			req.ID = id
			req.Resume = func() ([]string, error) { t.Fatal("focused session must not resolve/launch resume"); return nil, nil }
			status, err := (Client{Run: f.run}).Open(context.Background(), req)
			if err != nil || !strings.Contains(status, "internal OpenCode tab") {
				t.Fatalf("status=%q err=%v", status, err)
			}
			actions := f.actions()
			if len(actions) != 1 || actions[0][2] != "agent.focus" || actions[0][3] != `{"terminal":"t1"}` {
				t.Fatalf("actions: %v", actions)
			}
		})
	}
}

func TestNewTabUsesLiteralArgvAndExplicitLocalSpace(t *testing.T) {
	f := fixture()
	req := request(t)
	req.ID = "ses_exact_suffix"
	want := []string{`C:\Program Files\OpenCode\opencode.exe`, "--session", `id'; echo nope; #`}
	req.Resume = func() ([]string, error) { return want, nil }
	_, err := (Client{Run: f.run}).Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	actions := f.actions()
	if len(actions) != 1 || actions[0][2] != "tab.new" {
		t.Fatalf("actions: %v", actions)
	}
	var payload struct {
		Space, Name, Folder string
		Argv                []string
	}
	if err := json.Unmarshal([]byte(actions[0][3]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Space != "local" || payload.Name != req.Name || payload.Folder != req.Folder || !reflect.DeepEqual(payload.Argv, want) {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestSnapshotScopesProviderLocalityAndStaleRecords(t *testing.T) {
	f := fixture()
	f.responses["hc ls --json"] = `{"terminals":[
	{"address":"remote/t1","agent":"opencode","state":"live"},
	{"address":"t_remote","machine":"remote","agent":"opencode","state":"live"},
	{"address":"t_pi","agent":"pi","state":"live"},
	{"address":"t_finished","agent":"opencode","state":"finished"},
	{"address":"t_shell","agent":"opencode","state":"live"},
	{"address":"t_gone","agent":"opencode","state":"live"},
	{"address":"t1","agent":"opencode","state":"live"}]}`
	f.responses["hc get t_shell --json"] = `{"address":"t_shell","state":"live","status":null,"records":{"stale":{"app":"opencode","state":"done"}}}`
	f.responses["hc get t_gone --json"] = `{"address":"t_gone","state":"finished","records":{"stale":{"app":"opencode","state":"done"}}}`
	f.responses["hc get t1 --json"] = `{"address":"t1","state":"live","status":{"app":"opencode"},"records":{"exact":{"app":"opencode","state":"done"},"other":{"app":"pi","state":"working"},"stale":{"app":"opencode","state":"finished"}}}`
	live, err := (Client{Run: f.run}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(live, map[string][]string{"exact": {"t1"}}) {
		t.Fatalf("live=%v", live)
	}
	for _, call := range f.calls {
		if len(call) > 2 && call[1] == "get" && (strings.Contains(call[2], "remote") || call[2] == "t_pi" || call[2] == "t_finished") {
			t.Fatalf("probed excluded terminal: %v", call)
		}
	}
}

func TestUnknownProbeNeverLaunches(t *testing.T) {
	for _, tc := range []struct {
		name, key, out string
		failure        error
	}{
		{"core unavailable", "hc server status --json", "", exec.ErrNotFound},
		{"core malformed", "hc server status --json", `{`, nil},
		{"core absent", "hc server status --json", `{}`, nil},
		{"list exit", "hc ls --json", "", errors.New("exit status 1")},
		{"list malformed", "hc ls --json", `{`, nil},
		{"list missing", "hc ls --json", `{}`, nil},
		{"terminal address missing", "hc ls --json", `{"terminals":[{"agent":"opencode","state":"live"}]}`, nil},
		{"terminal unknown state", "hc ls --json", `{"terminals":[{"address":"t1","agent":"opencode","state":"unknown"}]}`, nil},
		{"get timeout", "hc get t1 --json", "", context.DeadlineExceeded},
		{"get malformed", "hc get t1 --json", `[]`, nil},
		{"get wrong identity", "hc get t1 --json", `{"address":"t2","state":"live"}`, nil},
		{"get unknown state", "hc get t1 --json", `{"address":"t1","state":"unknown"}`, nil},
		{"get missing status", "hc get t1 --json", `{"address":"t1","state":"live","records":{}}`, nil},
		{"get incomplete status", "hc get t1 --json", `{"address":"t1","state":"live","status":{},"records":{}}`, nil},
		{"records missing", "hc get t1 --json", `{"address":"t1","state":"live","status":{"app":"opencode"}}`, nil},
		{"record unknown state", "hc get t1 --json", `{"address":"t1","state":"live","status":{"app":"opencode"},"records":{"ses_exact":{"app":"opencode","state":"?"}}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture()
			f.responses[tc.key] = tc.out
			f.fail[tc.key] = tc.failure
			req := request(t)
			req.Resume = func() ([]string, error) { t.Fatal("unknown is not absent"); return nil, nil }
			_, err := (Client{Run: f.run}).Open(context.Background(), req)
			if err == nil || len(f.actions()) != 0 {
				t.Fatalf("err=%v actions=%v", err, f.actions())
			}
			if strings.HasPrefix(tc.key, "hc server") && len(f.calls) != 1 {
				t.Fatalf("probed after unavailable core: %v", f.calls)
			}
		})
	}
}

func TestAmbiguousMatchDoesNothing(t *testing.T) {
	f := fixture()
	f.responses["hc ls --json"] = `{"terminals":[{"address":"t1","agent":"opencode","state":"live"},{"address":"t2","agent":"opencode","state":"live"}]}`
	f.responses["hc get t2 --json"] = strings.ReplaceAll(f.responses["hc get t1 --json"], `"t1"`, `"t2"`)
	_, err := (Client{Run: f.run}).Open(context.Background(), request(t))
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || len(f.actions()) != 0 {
		t.Fatalf("err=%v actions=%v", err, f.actions())
	}
}

func TestUnsupportedProviderDoesNotProbe(t *testing.T) {
	f := fixture()
	req := request(t)
	req.Provider = "pi"
	_, err := (Client{Run: f.run}).Open(context.Background(), req)
	if err == nil || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestFocusFailureNeverFallsBackToLaunch(t *testing.T) {
	for _, out := range []string{`{"error":"window unavailable"}`, `{`, `null`} {
		f := fixture()
		f.responses[`herdr-app do agent.focus {"terminal":"t1"}`] = out
		req := request(t)
		req.Resume = func() ([]string, error) { t.Fatal("focus failed; must not launch"); return nil, nil }
		_, err := (Client{Run: f.run}).Open(context.Background(), req)
		if err == nil || len(f.actions()) != 1 {
			t.Fatalf("err=%v actions=%v", err, f.actions())
		}
	}
	f := fixture()
	f.fail[`herdr-app do agent.focus {"terminal":"t1"}`] = exec.ErrNotFound
	if _, err := (Client{Run: f.run}).Open(context.Background(), request(t)); err == nil {
		t.Fatal("missing herdr-app must fail")
	}
	if len(f.actions()) != 1 {
		t.Fatalf("actions=%v", f.actions())
	}
}

func TestNoVerifiedLocalSpaceNeverLaunches(t *testing.T) {
	for _, out := range []string{`{}`, `{"spaces":[]}`, `{"spaces":[{"id":"remote","machine":"vps","active":true}]}`, `{"spaces":[{"id":"unknown"}]}`, `garbage`} {
		f := fixture()
		f.responses["herdr-app ls"] = out
		req := request(t)
		req.ID = "not_live"
		_, err := (Client{Run: f.run}).Open(context.Background(), req)
		if err == nil || len(f.actions()) != 0 {
			t.Fatalf("out=%s err=%v actions=%v", out, err, f.actions())
		}
	}
}

func TestActiveLocalSpacePreferred(t *testing.T) {
	f := fixture()
	f.responses["herdr-app ls"] = `{"spaces":[{"id":"first","machine":null},{"id":"active","machine":null,"active":true}]}`
	req := request(t)
	req.ID = "not_live"
	if _, err := (Client{Run: f.run}).Open(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.actions()[0][3], `"space":"active"`) {
		t.Fatalf("actions=%v", f.actions())
	}
}

func TestNativeResumeFailureNeverLaunches(t *testing.T) {
	f := fixture()
	req := request(t)
	req.ID = "not_live"
	req.Resume = func() ([]string, error) { return nil, errors.New("native command unavailable") }
	if _, err := (Client{Run: f.run}).Open(context.Background(), req); err == nil {
		t.Fatal("missing native command must fail")
	}
	if len(f.actions()) != 0 {
		t.Fatalf("actions=%v", f.actions())
	}
}

// This executable fixture uses the Go test binary, so exit/timeout behavior is
// exercised natively on Windows too, with no sh/chmod dependency or real Herdr.
func TestHerdrHelperProcess(t *testing.T) {
	if os.Getenv("ANOTHER_HERDR_HELPER") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "exit":
		os.Exit(7)
	case "stderr":
		fmt.Fprint(os.Stderr, "window unavailable: meaningful diagnostic")
		os.Exit(7)
	case "huge-stderr":
		fmt.Fprint(os.Stderr, "window unavailable: "+strings.Repeat("x", 16*maxErrorStderr))
		os.Exit(7)
	case "wait":
		time.Sleep(time.Minute)
	default:
		fmt.Print(`{"ok":true}`)
	}
	os.Exit(0)
}

func TestCommandExecutionMissingExitTimeoutAndJSON(t *testing.T) {
	t.Setenv("ANOTHER_HERDR_HELPER", "1")
	client := Client{}
	if _, err := client.run(context.Background(), filepath.Join(t.TempDir(), "missing-herdr")); err == nil {
		t.Fatal("missing executable succeeded")
	}
	if _, err := client.run(context.Background(), os.Args[0], "-test.run=^TestHerdrHelperProcess$", "--", "exit"); err == nil {
		t.Fatal("exit failure succeeded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.run(ctx, os.Args[0], "-test.run=^TestHerdrHelperProcess$", "--", "wait"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	var reply struct{ OK bool }
	if err := client.read(context.Background(), os.Args[0], &reply, "-test.run=^TestHerdrHelperProcess$", "--", "json"); err != nil || !reply.OK {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
}

func TestCommandFailurePreservesBoundedStderr(t *testing.T) {
	t.Setenv("ANOTHER_HERDR_HELPER", "1")
	for _, mode := range []string{"stderr", "huge-stderr"} {
		t.Run(mode, func(t *testing.T) {
			_, err := (Client{}).run(context.Background(), os.Args[0], "-test.run=^TestHerdrHelperProcess$", "--", mode)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 7 {
				t.Fatalf("lost native exit error: %v", err)
			}
			if !strings.Contains(err.Error(), "window unavailable:") {
				t.Fatalf("lost stderr: %v", err)
			}
			if mode == "stderr" && !strings.Contains(err.Error(), "meaningful diagnostic") {
				t.Fatalf("lost diagnostic: %v", err)
			}
			if mode == "huge-stderr" {
				if !strings.HasSuffix(err.Error(), " [truncated]") || strings.Count(err.Error(), "x") > maxErrorStderr+100 {
					t.Fatalf("stderr not bounded: %d-byte error", len(err.Error()))
				}
			}
		})
	}
}

func TestPartialSnapshotKeepsExactPositivesButRefusesUnknownAbsence(t *testing.T) {
	for _, kind := range []string{"missing records", "unavailable detail", "malformed detail"} {
		for _, unknownFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/unknownFirst=%v", kind, unknownFirst), func(t *testing.T) {
				f := fixture()
				known := `{"address":"t1","agent":"opencode","state":"live"}`
				unknown := `{"address":"t_bad","agent":"opencode","state":"live"}`
				entries := known + "," + unknown
				if unknownFirst {
					entries = unknown + "," + known
				}
				f.responses["hc ls --json"] = `{"terminals":[` + entries + `]}`
				f.responses["hc get t_bad --json"] = `{"address":"t_bad","state":"live","status":{"app":"opencode","state":"idle"}}`
				switch kind {
				case "unavailable detail":
					f.fail["hc get t_bad --json"] = errors.New("terminal metadata unavailable")
				case "malformed detail":
					f.responses["hc get t_bad --json"] = "{"
				}
				client := Client{Run: f.run}
				live, err := client.Snapshot(context.Background())
				if err == nil || !reflect.DeepEqual(live["ses_exact"], []string{"t1"}) {
					t.Fatalf("partial snapshot: live=%v err=%v", live, err)
				}
				req := request(t)
				req.Resume = func() ([]string, error) {
					t.Fatal("partial knowledge must never launch native resume")
					return nil, nil
				}
				status, err := client.Open(context.Background(), req)
				if err != nil || !strings.Contains(status, "Focused Herdr terminal t1") {
					t.Fatalf("known exact focus: status=%q err=%v", status, err)
				}
				if actions := f.actions(); len(actions) != 1 || actions[0][2] != "agent.focus" {
					t.Fatalf("actions=%v", actions)
				}
				f.calls = nil
				req.ID = "saved_not_live"
				_, err = client.Open(context.Background(), req)
				if err == nil || !strings.Contains(err.Error(), "terminal t_bad native metadata incomplete") || !strings.Contains(err.Error(), "hc get t_bad --json") || !strings.Contains(err.Error(), "restore native session reporting") {
					t.Fatalf("missing actionable incomplete-identity error: %v", err)
				}
				if len(f.actions()) != 0 {
					t.Fatalf("absence was inferred from partial knowledge: %v", f.actions())
				}
			})
		}
	}
}

func TestPartialSnapshotStillRefusesKnownAmbiguousMatches(t *testing.T) {
	f := fixture()
	f.responses["hc ls --json"] = `{"terminals":[{"address":"t_bad","agent":"opencode","state":"live"},{"address":"t1","agent":"opencode","state":"live"},{"address":"t2","agent":"opencode","state":"live"}]}`
	f.responses["hc get t_bad --json"] = `{"address":"t_bad","state":"live","status":{"app":"opencode","state":"idle"}}`
	f.responses["hc get t2 --json"] = strings.ReplaceAll(f.responses["hc get t1 --json"], `"t1"`, `"t2"`)
	_, err := (Client{Run: f.run}).Open(context.Background(), request(t))
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || len(f.actions()) != 0 {
		t.Fatalf("err=%v actions=%v", err, f.actions())
	}
}
