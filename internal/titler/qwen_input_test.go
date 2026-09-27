package titler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/titler"
)

// The npm CMD shim used on Windows drops a multiline argv prompt after its
// first newline. Exercise that real process boundary rather than a shell-only
// fake, and inspect the bytes the CLI receives before any model call.
func TestSuggestQwenPreservesPrompt(t *testing.T) {
	for _, modelID := range []string{"", "qwen3-coder-plus"} {
		t.Run("model="+modelID, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "agent bin")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(dir, "input.json")
			t.Setenv("ANOTHER_QWEN_TEST_EXE", exe)
			t.Setenv("ANOTHER_QWEN_CAPTURE", capture)
			name := "qwen"
			script := "#!/bin/sh\nexec \"$ANOTHER_QWEN_TEST_EXE\" -test.run=^TestQwenInputHelper$ -- \"$@\"\n"
			if runtime.GOOS == "windows" {
				name = "qwen.cmd"
				script = "@ECHO OFF\r\n\"%ANOTHER_QWEN_TEST_EXE%\" -test.run=^TestQwenInputHelper$ -- %*\r\n"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			const content = "修复标题建议\n保留中文、\"引号\"、%PATH%、!name!、& | < > 和 C:\\project\\file"
			got, err := titler.Suggest(context.Background(), titler.Config{Provider: "qwen", Model: modelID}, titler.Request{
				CreatedAt: time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC),
				Messages:  []model.Message{{Role: model.RoleUser, Content: content}},
			})
			if err != nil || got != "0903｜修复｜标题输入传递" {
				t.Fatalf("Suggest = %q, %v", got, err)
			}
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var received qwenInput
			if err := json.Unmarshal(raw, &received); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"MMDD is fixed to 0903", "MMDD｜类型｜主题", "<<<SESSION\nuser: " + content + "\nSESSION\n"} {
				if !strings.Contains(received.Stdin, want) {
					t.Errorf("Qwen stdin missing %q; received %q", want, received.Stdin)
				}
			}
			wantArgs := []string{"--safe-mode", "--chat-recording=false", "--output-format", "text"}
			if modelID != "" {
				wantArgs = append(wantArgs, "--model", modelID)
			}
			if !reflect.DeepEqual(received.Args, wantArgs) {
				t.Errorf("Qwen args = %q, want %q", received.Args, wantArgs)
			}
		})
	}
}

type qwenInput struct {
	Args  []string
	Stdin string
}

func TestQwenInputHelper(t *testing.T) {
	path := os.Getenv("ANOTHER_QWEN_CAPTURE")
	if path == "" {
		t.Skip("subprocess helper")
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	data, err := json.Marshal(qwenInput{Args: args, Stdin: string(input)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("0903｜修复｜标题输入传递")
	os.Exit(0)
}
