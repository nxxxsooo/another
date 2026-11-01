package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/config"
)

func TestQwenConfigDirIsTheConfigHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("QWEN_HOME", filepath.Join(home, ".qwen"))
	if got := QwenConfigDir(); got != filepath.Join(home, ".qwen") {
		t.Fatalf("QwenConfigDir() = %q, want %q", got, filepath.Join(home, ".qwen"))
	}

	_ = os.Unsetenv("QWEN_HOME")
	wantDefault := filepath.Join(config.HomeDir(), ".qwen")
	if got := QwenConfigDir(); got != wantDefault {
		t.Fatalf("QwenConfigDir() = %q, want %q", got, wantDefault)
	}

	override := t.TempDir()
	t.Setenv(QwenConfigDirEnv, override)
	if got := QwenConfigDir(); got != override {
		t.Fatalf("QwenConfigDir() = %q, want the override %q", got, override)
	}
}

// The adapter owns one hook entry inside settings.json. Every foreign key,
// hook event, and foreign Stop hook must survive both an install and a removal
// untouched — hindsight's coding-agents hooks are the real coexistence case.
func TestQwenInstallMergesWithoutTouchingForeignSettings(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "settings.json")
	before := `{
  "env": {
    "BAILIAN_KEY": "sk-123456"
  },
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {"type": "command", "command": "node start.js", "timeout": 30000}
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "custom",
        "hooks": [
          {"type": "command", "command": "node elsewhere.js", "timeout": 60000}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(settings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	status := QwenStatus(root)
	if status.State != StateMissing {
		t.Fatalf("state = %q, want missing", status.State)
	}
	installed, err := InstallQwen(status, "v9.9.9", "zh", false)
	if err != nil {
		t.Fatal(err)
	}
	if installed.State != StateCurrent {
		t.Fatalf("state = %q, want current", installed.State)
	}
	if filepath.Base(installed.Dir) != "settings.json" {
		t.Fatalf("Dir = %q, want settings.json", installed.Dir)
	}

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("rewritten settings.json does not parse: %v", err)
	}
	if _, ok := doc["env"]; !ok {
		t.Fatal("the foreign env block did not survive the install")
	}

	var hooksDoc map[string]json.RawMessage
	if err := json.Unmarshal(doc["hooks"], &hooksDoc); err != nil {
		t.Fatalf("hooks block does not parse: %v", err)
	}
	if _, ok := hooksDoc["SessionStart"]; !ok {
		t.Fatal("the foreign SessionStart hooks did not survive the install")
	}

	var stopEntries []qwenMatcherEntry
	if err := json.Unmarshal(hooksDoc["Stop"], &stopEntries); err != nil {
		t.Fatalf("Stop entries do not parse: %v", err)
	}
	if len(stopEntries) != 2 {
		t.Fatalf("Stop entries count = %d, want 2 (foreign + another)", len(stopEntries))
	}
	hasForeign := false
	hasAnother := false
	for _, entry := range stopEntries {
		for _, h := range entry.Hooks {
			if h.Command == "node elsewhere.js" {
				hasForeign = true
			}
			if h.Command == QwenCommand && h.Name == QwenName {
				hasAnother = true
			}
		}
	}
	if !hasForeign {
		t.Fatal("foreign Stop hook did not survive the install")
	}
	if !hasAnother {
		t.Fatal("another Stop hook was not installed")
	}

	// Removing takes back only the hook another wrote.
	if err := RemoveQwen(QwenStatus(root), false); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]json.RawMessage
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("settings.json after removal does not parse: %v", err)
	}
	if _, ok := after["env"]; !ok {
		t.Fatal("the foreign env block did not survive the removal")
	}
	var afterHooks map[string]json.RawMessage
	if err := json.Unmarshal(after["hooks"], &afterHooks); err != nil {
		t.Fatalf("hooks block after removal does not parse: %v", err)
	}
	if _, ok := afterHooks["SessionStart"]; !ok {
		t.Fatal("foreign SessionStart hooks did not survive the removal")
	}
	var afterStop []qwenMatcherEntry
	if err := json.Unmarshal(afterHooks["Stop"], &afterStop); err != nil {
		t.Fatalf("Stop entries after removal do not parse: %v", err)
	}
	if len(afterStop) != 1 || afterStop[0].Hooks[0].Command != "node elsewhere.js" {
		t.Fatalf("after removal Stop entries = %+v, want foreign hook only", afterStop)
	}

	if runtime.GOOS != "windows" {
		if info, err := os.Stat(settings); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode after removal = %v, %v", info.Mode().Perm(), err)
		}
	}
}

// A hook someone added beside another's keeps every field it has, including
// ones another does not model, when another's own hook is removed.
func TestQwenRemoveKeepsUnknownFieldsOfNeighbouringHooks(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "settings.json")
	body := `{"hooks":{"Stop":[{"matcher":"m","note":"mine","hooks":[` +
		`{"type":"command","command":"node keep.js","name":"keep","env":{"A":"1"},"description":"stays"},` +
		`{"type":"command","command":"` + QwenCommand + `","name":"` + QwenName + `"}]}]}}`
	if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveQwen(QwenStatus(root), false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings)
	for _, want := range []string{`"description": "stays"`, `"A": "1"`, `"note": "mine"`, `"matcher": "m"`, "node keep.js"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("remove lost %s:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), QwenCommand) {
		t.Fatalf("another's hook survived:\n%s", data)
	}
}

// A Stop value another cannot read is refused rather than replaced.
func TestQwenInstallRefusesAStopValueItCannotExtend(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "settings.json")
	body := `{"hooks":{"Stop":{"not":"an array"}}}`
	if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallQwen(QwenStatus(root), "dev", "", true); err == nil {
		t.Fatal("install replaced a Stop value it could not read")
	}
	if data, _ := os.ReadFile(settings); string(data) != body {
		t.Fatalf("file changed:\n%s", data)
	}
}

func TestQwenStatusRefusesWhatItDidNotWrite(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "settings.json")
	hand := `{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {"type": "command", "command": "echo mine", "name": "` + QwenName + `", "timeout": 5}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(settings, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}

	status := QwenStatus(root)
	if status.State != StateForeign {
		t.Fatalf("state = %q, want foreign", status.State)
	}
	if _, err := InstallQwen(status, "v9.9.9", "zh", false); err == nil {
		t.Fatal("install overwrote a foreign hook without being told to")
	}
	if _, err := InstallQwen(status, "v9.9.9", "zh", true); err != nil {
		t.Fatalf("a forced install should claim the hook: %v", err)
	}
	if got := QwenStatus(root).State; got != StateCurrent {
		t.Fatalf("after a forced install the state = %q, want current", got)
	}

	// Removing a foreign hook stays refused without --force too.
	if err := os.WriteFile(settings, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveQwen(QwenStatus(root), false); err == nil {
		t.Fatal("removal took back a hook another did not write")
	}
	if err := RemoveQwen(QwenStatus(root), true); err != nil {
		t.Fatalf("a forced removal should drop the hook: %v", err)
	}
	if got := QwenStatus(root).State; got != StateMissing {
		t.Fatalf("after a forced removal the state = %q, want missing", got)
	}
}

func TestQwenInstallRefusesAnUnparseableSharedFile(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settings, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := QwenStatus(root)
	if status.State != StateForeign {
		t.Fatalf("state = %q, want foreign", status.State)
	}
	_, err := InstallQwen(status, "v9.9.9", "zh", true)
	if err == nil {
		t.Fatal("a forced install overwrote a file another cannot parse")
	}
	data, readErr := os.ReadFile(settings)
	if readErr != nil || string(data) != "{not json" {
		t.Fatalf("the file changed: %q, %v", data, readErr)
	}
}

func TestQwenAdapterIsRegistered(t *testing.T) {
	adapter, ok := Find(Qwen)
	if !ok {
		t.Fatal("the qwen adapter is not registered")
	}
	if adapter.Provider != "qwen" {
		t.Fatalf("Provider = %q, want qwen", adapter.Provider)
	}
	var saved config.Integrations
	p := adapter.Consent(&saved)
	*p = true
	if !saved.QwenTitlePolicy {
		t.Fatal("the consent field is not the qwen one")
	}
	found := false
	for _, provider := range Providers() {
		if provider == "qwen" {
			found = true
		}
	}
	if !found {
		t.Fatal("Providers() does not name qwen")
	}
	if adapter.Status == nil || adapter.Install == nil || adapter.Remove == nil {
		t.Fatal("the adapter's operations are not bound")
	}

	for _, name := range []string{"qwen", "qwen-code", "qwen_code"} {
		if _, ok := ForProvider(name); !ok {
			t.Fatalf("ForProvider(%q) failed", name)
		}
	}
}

func TestQwenInstallCreatesTheFile(t *testing.T) {
	root := t.TempDir()
	installed, err := InstallQwen(QwenStatus(root), "v9.9.9", "zh", false)
	if err != nil {
		t.Fatal(err)
	}
	if installed.State != StateCurrent {
		t.Fatalf("state = %q, want current", installed.State)
	}
	data, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "another rename") {
		t.Fatalf("the installed hook does not call another: %s", data)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(root, "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Fatalf("settings.json mode = %o, want 644", perm)
		}
	}
}
