package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/config"
)

// The hook lives in the shared hooks.json agy reads, which is a sibling of the
// agy home another's provider indexes — not inside it. Resolution is checked
// here rather than assumed, because installing into a file agy never reads
// looks exactly like a successful installation.
func TestAgyConfigDirIsTheConfigSiblingOfTheAgyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGY_HOME", filepath.Join(home, ".gemini", "antigravity-cli"))
	if got := AgyConfigDir(); got != filepath.Join(home, ".gemini", "config") {
		t.Fatalf("AgyConfigDir() = %q, want the sibling config directory", got)
	}

	// Without AGY_HOME the default root implies the same sibling.
	os.Unsetenv("AGY_HOME")
	t.Setenv("HOME", home)
	if got := AgyConfigDir(); got != filepath.Join(home, ".gemini", "config") {
		t.Fatalf("AgyConfigDir() = %q, want ~/.gemini/config under HOME", got)
	}

	// The adapter's own override still wins: it exists for an agy another
	// cannot locate at all.
	override := t.TempDir()
	t.Setenv(AgyConfigDirEnv, override)
	if got := AgyConfigDir(); got != override {
		t.Fatalf("AgyConfigDir() = %q, want the override %q", got, override)
	}
}

// The adapter owns one group inside a file other tools also write. Every
// foreign group must survive both an install and a removal untouched —
// hindsight's coding-agents group is the real coexistence case.
func TestAgyInstallMergesWithoutTouchingForeignGroups(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks.json")
	before := `{
  "coding-agents": {
    "Stop": [
      {"command": "node elsewhere.js", "timeout": 30}
    ]
  }
}`
	if err := os.WriteFile(hooks, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	status := AgyStatus(root)
	if status.State != StateMissing {
		t.Fatalf("state = %q, want missing", status.State)
	}
	installed, err := InstallAgy(status, "v9.9.9", "zh", false)
	if err != nil {
		t.Fatal(err)
	}
	if installed.State != StateCurrent {
		t.Fatalf("state = %q, want current", installed.State)
	}
	if filepath.Base(installed.Dir) != "hooks.json" {
		t.Fatalf("Dir = %q, want the shared hooks.json", installed.Dir)
	}

	data, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("rewritten hooks.json does not parse: %v", err)
	}
	if _, ok := doc["coding-agents"]; !ok {
		t.Fatal("the foreign coding-agents group did not survive the install")
	}
	group, ok := doc[AgyGroup].(map[string]any)
	if !ok {
		t.Fatalf("group %q missing after install", AgyGroup)
	}
	if _, ok := group["Stop"]; !ok {
		t.Fatal("the installed group carries no Stop hook")
	}

	// Removing takes back only the group another wrote.
	if err := RemoveAgy(AgyStatus(root), false); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("hooks.json after removal does not parse: %v", err)
	}
	if _, ok := after[AgyGroup]; ok {
		t.Fatal("the group survived a removal")
	}
	if _, ok := after["coding-agents"]; !ok {
		t.Fatal("the foreign coding-agents group did not survive the removal")
	}
	// The mode the file had before the install is not another's to widen:
	// a private hooks.json stays private through an install and a removal.
	if info, err := os.Stat(hooks); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode after removal = %v, %v", info.Mode().Perm(), err)
	}
}

// A group named another-title-policy with a different command was not written
// by this binary: either a hand edit or an old another. It is reported, never
// overwritten silently, and its removal needs to be told to.
func TestAgyStatusRefusesWhatItDidNotWrite(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks.json")
	hand := `{"` + AgyGroup + `": {"Stop": [{"command": "echo mine", "timeout": 5}]}}`
	if err := os.WriteFile(hooks, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}

	status := AgyStatus(root)
	if status.State != StateForeign {
		t.Fatalf("state = %q, want foreign", status.State)
	}
	if _, err := InstallAgy(status, "v9.9.9", "zh", false); err == nil {
		t.Fatal("install overwrote a foreign group without being told to")
	}
	if _, err := InstallAgy(status, "v9.9.9", "zh", true); err != nil {
		t.Fatalf("a forced install should claim the group: %v", err)
	}
	if got := AgyStatus(root).State; got != StateCurrent {
		t.Fatalf("after a forced install the state = %q, want current", got)
	}

	// Removing a foreign group stays refused without --force too.
	if err := os.WriteFile(hooks, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAgy(AgyStatus(root), false); err == nil {
		t.Fatal("removal took back a group another did not write")
	}
	if err := RemoveAgy(AgyStatus(root), true); err != nil {
		t.Fatalf("a forced removal should drop the group: %v", err)
	}
	if got := AgyStatus(root).State; got != StateMissing {
		t.Fatalf("after a forced removal the state = %q, want missing", got)
	}
}

// A hooks.json another cannot parse holds someone's work another cannot
// preserve by merging. No flag makes that loss safe to do silently — the
// person has to move the file aside themselves, so even --force refuses.
func TestAgyInstallRefusesAnUnparseableSharedFile(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(hooks, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := AgyStatus(root)
	if status.State != StateForeign {
		t.Fatalf("state = %q, want foreign", status.State)
	}
	_, err := InstallAgy(status, "v9.9.9", "zh", true)
	if err == nil {
		t.Fatal("a forced install overwrote a file another cannot parse")
	}
	data, readErr := os.ReadFile(hooks)
	if readErr != nil || string(data) != "{not json" {
		t.Fatalf("the file changed: %q, %v", data, readErr)
	}
}

// The adapter is one of the rows setup, the integrations command, and doctor
// draw; registration is what makes it appear everywhere at once.
func TestAgyAdapterIsRegistered(t *testing.T) {
	adapter, ok := Find(Agy)
	if !ok {
		t.Fatal("the agy adapter is not registered")
	}
	if adapter.Provider != "agy" {
		t.Fatalf("Provider = %q, want agy", adapter.Provider)
	}
	var saved config.Integrations
	p := adapter.Consent(&saved)
	*p = true
	if !saved.AgyTitlePolicy {
		t.Fatal("the consent field is not the agy one")
	}
	found := false
	for _, provider := range Providers() {
		if provider == "agy" {
			found = true
		}
	}
	if !found {
		t.Fatal("Providers() does not name agy")
	}
	if adapter.Status == nil || adapter.Install == nil || adapter.Remove == nil {
		t.Fatal("the adapter's operations are not bound")
	}
}

// A hooks.json another has to create from nothing lands parseable and with a
// sane mode, because agy reads it on the next turn.
func TestAgyInstallCreatesTheFile(t *testing.T) {
	root := t.TempDir()
	installed, err := InstallAgy(AgyStatus(root), "v9.9.9", "zh", false)
	if err != nil {
		t.Fatal(err)
	}
	if installed.State != StateCurrent {
		t.Fatalf("state = %q, want current", installed.State)
	}
	data, err := os.ReadFile(filepath.Join(root, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "another rename") {
		t.Fatalf("the installed hook does not call another: %s", data)
	}
	info, err := os.Stat(filepath.Join(root, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("hooks.json mode = %o, want 644", perm)
	}
}
