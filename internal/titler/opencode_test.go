package titler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/titler"
)

type openCodeCall struct {
	Args []string
	Dir  string
}

// A native executable fixture exercises argv and filesystem paths on Windows
// as well as Unix, without a shell or a real OpenCode service/model call.
func TestMain(m *testing.M) {
	if capture := os.Getenv("ANOTHER_OPENCODE_TEST_CAPTURE"); capture != "" && len(os.Args) > 1 && (os.Args[1] == "run" || os.Args[1] == "api") {
		dir, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		data, err := json.Marshal(openCodeCall{Args: os.Args[1:], Dir: dir})
		if err != nil {
			panic(err)
		}
		f, err := os.OpenFile(capture, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			panic(err)
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
		if os.Args[1] == "api" {
			if id, ok := strings.CutPrefix(os.Args[3], "/api/session/"); ok {
				switch os.Getenv("ANOTHER_OPENCODE_TEST_DELETE") {
				case "missing":
					fmt.Printf("{\"_tag\":\"SessionNotFoundError\",\"sessionID\":%q}\n", id)
					fmt.Fprintln(os.Stderr, "HTTP 404 Not Found")
					os.Exit(1)
				case "fail":
					// A CLI can print an HTTP error yet exit successfully.
					fmt.Fprintln(os.Stderr, "HTTP 500 deletion rejected")
				}
			}
			os.Exit(0)
		}
		if notify := os.Getenv("ANOTHER_OPENCODE_TEST_CANCEL_URL"); notify != "" {
			resp, err := http.Get(notify)
			if err == nil {
				_ = resp.Body.Close()
			}
			time.Sleep(time.Minute)
			os.Exit(1)
		}
		if os.Getenv("ANOTHER_OPENCODE_TEST_RUN_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "model rejected")
			os.Exit(1)
		}
		if os.Getenv("ANOTHER_OPENCODE_TEST_WRITE") == "1" {
			if err := os.WriteFile("unexpected-output", []byte("fixture"), 0o600); err != nil {
				panic(err)
			}
		}
		fmt.Println("0903｜Fix｜Release title services")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSuggestCleansUpOpenCode(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "title cache & 中文 + #")
	if err := os.Mkdir(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HOME", "LOCALAPPDATA", "XDG_CACHE_HOME"} {
		t.Setenv(name, cacheRoot)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(cache, "another", "another-titler-opencode")
	idPattern := regexp.MustCompile(`^ses_[0-9a-f]{32}$`)
	seenIDs := make(map[string]bool)
	for _, provider := range []string{"opencode", "opencode2"} {
		for _, scenario := range []string{"success", "run failure", "cancel", "delete failure", "both fail", "unexpected write"} {
			t.Run(provider+"/"+scenario, func(t *testing.T) {
				binDir := filepath.Join(t.TempDir(), "agent bin")
				if err := os.Mkdir(binDir, 0o700); err != nil {
					t.Fatal(err)
				}
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(exe)
				if err != nil {
					t.Fatal(err)
				}
				name := provider
				if runtime.GOOS == "windows" {
					name += ".exe"
				}
				if err := os.WriteFile(filepath.Join(binDir, name), data, 0o700); err != nil {
					t.Fatal(err)
				}
				capture := filepath.Join(t.TempDir(), "calls.jsonl")
				t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("ANOTHER_OPENCODE_TEST_CAPTURE", capture)
				t.Setenv("ANOTHER_OPENCODE_TEST_RUN_FAIL", "0")
				t.Setenv("ANOTHER_OPENCODE_TEST_WRITE", "0")
				t.Setenv("ANOTHER_OPENCODE_TEST_DELETE", "")
				t.Setenv("ANOTHER_OPENCODE_TEST_CANCEL_URL", "")
				if scenario == "unexpected write" {
					t.Setenv("ANOTHER_OPENCODE_TEST_WRITE", "1")
					t.Cleanup(func() { _ = os.Remove(filepath.Join(workDir, "unexpected-output")) })
				}
				if scenario == "run failure" || scenario == "both fail" {
					t.Setenv("ANOTHER_OPENCODE_TEST_RUN_FAIL", "1")
				}
				if scenario == "run failure" {
					t.Setenv("ANOTHER_OPENCODE_TEST_DELETE", "missing")
				}
				if scenario == "delete failure" || scenario == "both fail" {
					t.Setenv("ANOTHER_OPENCODE_TEST_DELETE", "fail")
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if scenario == "cancel" {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						cancel()
						w.WriteHeader(http.StatusNoContent)
					}))
					defer server.Close()
					t.Setenv("ANOTHER_OPENCODE_TEST_CANCEL_URL", server.URL)
				}
				got, err := titler.Suggest(ctx, titler.Config{Provider: provider, Model: "test/title-model", Language: titler.LangEnglish}, titler.Request{CreatedAt: time.Now()})
				if scenario == "success" {
					if err != nil || got != "0903｜Fix｜Release title services" {
						t.Fatalf("Suggest = %q, %v", got, err)
					}
				} else if err == nil || got != "" {
					t.Fatalf("Suggest = %q, %v; want failure without suggestion", got, err)
				}
				for marker, want := range map[string]bool{
					"model rejected":       scenario == "run failure" || scenario == "both fail",
					"timed out":            scenario == "cancel",
					"deletion rejected":    scenario == "delete failure" || scenario == "both fail",
					"SessionNotFoundError": false,
					"not empty":            scenario == "unexpected write",
				} {
					if found := err != nil && strings.Contains(err.Error(), marker); found != want {
						t.Errorf("error %v contains %q = %t; want %t", err, marker, found, want)
					}
				}
				raw, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
				if len(lines) != 2 {
					t.Fatalf("want run and session deletion only, got %s", raw)
				}
				var run, remove openCodeCall
				for i, call := range []*openCodeCall{&run, &remove} {
					if err := json.Unmarshal([]byte(lines[i]), call); err != nil {
						t.Fatal(err)
					}
				}
				if len(run.Args) != 9 || !slices.Equal(run.Args[:5], []string{"run", "--archived", "--title", "another title suggestion", "--session"}) ||
					!slices.Equal(run.Args[6:8], []string{"--model", "test/title-model"}) || !titler.IsGeneratedPrompt(run.Args[8]) {
					t.Fatalf("run arguments = %q", run.Args)
				}
				id := run.Args[5]
				if !idPattern.MatchString(id) || seenIDs[id] {
					t.Fatalf("invalid or reused session ID %q", id)
				}
				seenIDs[id] = true
				if !slices.Equal(remove.Args, []string{"api", "DELETE", "/api/session/" + id}) || remove.Dir != run.Dir {
					t.Fatalf("deletion must target the run's exact session in its directory: run=%+v delete=%+v", run, remove)
				}
				resolved, err := filepath.EvalSymlinks(workDir)
				if err != nil {
					t.Fatal(err)
				}
				if run.Dir != resolved {
					t.Fatalf("run directory = %q; want shared %q", run.Dir, resolved)
				}
				if !titler.IsGeneratedSession("unrelated title", run.Dir) {
					t.Fatalf("leftover not recognized: %q", run.Dir)
				}
				if scenario == "unexpected write" {
					if _, err := os.Stat(filepath.Join(run.Dir, "unexpected-output")); err != nil {
						t.Fatal(err)
					}
					if got, err := titler.Suggest(ctx, titler.Config{Provider: provider}, titler.Request{CreatedAt: time.Now()}); got != "" || err == nil || !strings.Contains(err.Error(), "not empty") {
						t.Fatalf("Suggest in a nonempty directory = %q, %v", got, err)
					}
					if after, err := os.ReadFile(capture); err != nil || string(after) != string(raw) {
						t.Fatalf("CLI was invoked from a nonempty directory: %q, %v", after, err)
					}
				} else if entries, err := os.ReadDir(run.Dir); err != nil || len(entries) != 0 {
					t.Fatalf("shared directory entries = %v, %v; want empty", entries, err)
				}
			})
		}
	}
}

// Explicit opt-in only: creates synthetic title-agent sessions, never reads or
// renames user sessions.
func TestOpenCodeTitlerLive(t *testing.T) {
	if os.Getenv("ANOTHER_TEST_OPENCODE_LIVE") != "1" {
		t.Skip("set ANOTHER_TEST_OPENCODE_LIVE=1 to invoke the installed OpenCode")
	}
	for _, provider := range []string{"opencode", "opencode2"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			got, err := titler.Suggest(context.Background(), titler.Config{Provider: provider, Model: os.Getenv("ANOTHER_TEST_OPENCODE_MODEL"), Language: titler.LangEnglish}, titler.Request{
				CreatedAt: time.Now(),
				Messages:  []model.Message{{Role: model.RoleUser, Content: "Fix a synthetic example that leaks temporary title services. This is a harmless title-generation smoke test."}},
			})
			if err != nil || got == "" {
				t.Fatalf("Suggest = %q, %v; want a title", got, err)
			}
			t.Logf("suggestion: %q", got)
		})
	}
}
