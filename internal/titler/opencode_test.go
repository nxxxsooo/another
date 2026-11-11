package titler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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
			if os.Getenv("ANOTHER_OPENCODE_TEST_DISPOSE_FAIL") == "1" {
				fmt.Fprintln(os.Stderr, "disposal rejected")
				os.Exit(1)
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
		fmt.Println("0903｜Fix｜Release title services")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSuggestDisposesOpenCodeLocation(t *testing.T) {
	tempRoot := filepath.Join(t.TempDir(), "title cache & 中文 + #")
	if err := os.Mkdir(tempRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tempRoot)
	}
	for _, provider := range []string{"opencode", "opencode2"} {
		for _, scenario := range []string{"success", "run failure", "cancel", "dispose failure", "both fail"} {
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
				t.Setenv("ANOTHER_OPENCODE_TEST_DISPOSE_FAIL", "0")
				t.Setenv("ANOTHER_OPENCODE_TEST_CANCEL_URL", "")
				if scenario == "run failure" || scenario == "both fail" {
					t.Setenv("ANOTHER_OPENCODE_TEST_RUN_FAIL", "1")
				}
				if scenario == "dispose failure" || scenario == "both fail" {
					t.Setenv("ANOTHER_OPENCODE_TEST_DISPOSE_FAIL", "1")
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
				for marker, required := range map[string]bool{
					"model rejected":    scenario == "run failure" || scenario == "both fail",
					"disposal rejected": scenario == "dispose failure" || scenario == "both fail",
				} {
					if required && !strings.Contains(err.Error(), marker) {
						t.Errorf("error %v lost %q", err, marker)
					}
				}
				raw, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
				if len(lines) != 2 {
					t.Fatalf("want run and disposal, got %s", raw)
				}
				var run, dispose openCodeCall
				if err := json.Unmarshal([]byte(lines[0]), &run); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(lines[1]), &dispose); err != nil {
					t.Fatal(err)
				}
				if len(run.Args) != 4 || run.Args[0] != "run" || run.Args[1] != "--model" || run.Args[2] != "test/title-model" || !titler.IsGeneratedPrompt(run.Args[3]) {
					t.Fatalf("run arguments = %q", run.Args)
				}
				if len(dispose.Args) != 3 || dispose.Args[0] != "api" || dispose.Args[1] != "DELETE" {
					t.Fatalf("disposal arguments = %q", dispose.Args)
				}
				u, err := url.Parse(dispose.Args[2])
				if err != nil {
					t.Fatal(err)
				}
				if u.Path != "/api/debug/location" || u.Query().Get("location[directory]") != run.Dir || dispose.Dir != run.Dir {
					t.Fatalf("disposal must target exactly the run directory: run=%+v dispose=%+v", run, dispose)
				}
				if !titler.IsGeneratedSession("unrelated title", run.Dir) {
					t.Fatalf("leftover not recognized: %q", run.Dir)
				}
				if _, err := os.Stat(run.Dir); !os.IsNotExist(err) {
					t.Fatalf("temporary directory remains: %v", err)
				}
			})
		}
	}
}

// Explicit opt-in only: creates synthetic title-agent sessions, never reads or
// renames user sessions. Run twice while observing shared-service children.
func TestOpenCodeTitlerLive(t *testing.T) {
	if os.Getenv("ANOTHER_TEST_OPENCODE_LIVE") != "1" {
		t.Skip("set ANOTHER_TEST_OPENCODE_LIVE=1 to invoke the installed OpenCode")
	}
	for i := 0; i < 2; i++ {
		got, err := titler.Suggest(context.Background(), titler.Config{Provider: "opencode", Language: titler.LangEnglish}, titler.Request{
			CreatedAt: time.Now(),
			Messages:  []model.Message{{Role: model.RoleUser, Content: "Fix a synthetic example that leaks temporary title services. This is a harmless title-generation smoke test."}},
		})
		if err != nil {
			t.Fatalf("invocation %d: %v", i+1, err)
		}
		t.Logf("invocation %d suggestion: %q", i+1, got)
	}
}
