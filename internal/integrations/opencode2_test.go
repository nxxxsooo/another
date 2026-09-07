package integrations_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	plugin "github.com/nxxxsooo/another/integrations/opencode2-title-policy"
	"github.com/nxxxsooo/another/internal/integrations"
)

// configDir isolates every test from the machine's real OpenCode 2, which the
// resolver would otherwise find by asking the CLI.
func configDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(integrations.ConfigDirEnv, dir)
	return dir
}

func install(t *testing.T, dir, version, language string) integrations.Status {
	t.Helper()
	status, err := integrations.InstallOpenCode2(integrations.OpenCode2Status(dir), version, language, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	return status
}

func TestInstallWritesPluginAndManifest(t *testing.T) {
	dir := configDir(t)
	status := install(t, dir, "1.2.3", "zh")
	if status.State != integrations.StateCurrent {
		t.Fatalf("state = %q, want current", status.State)
	}
	for name, want := range plugin.Files() {
		got, err := os.ReadFile(filepath.Join(status.Dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s on disk differs from the shipped file", name)
		}
	}
	if status.Language != "zh" || status.Version != "1.2.3" {
		t.Fatalf("manifest = %+v, want language zh and version 1.2.3", status)
	}
	// The plugin has to be where OpenCode 2 discovers it on its own, because
	// another never edits opencode.json(c).
	if want := filepath.Join(dir, "plugins", plugin.DirName); status.Dir != want {
		t.Fatalf("dir = %q, want %q", status.Dir, want)
	}
}

func TestStatusReportsMissingAndOutdated(t *testing.T) {
	dir := configDir(t)
	if got := integrations.OpenCode2Status(dir).State; got != integrations.StateMissing {
		t.Fatalf("state = %q, want missing", got)
	}
	status := install(t, dir, "1.0.0", "auto")
	// An upgrade is an older release's files, untouched since another wrote
	// them, next to a binary that now ships different ones.
	old := map[string]string{}
	for _, name := range plugin.Names() {
		content := []byte("// another 1.0.0\n")
		if err := os.WriteFile(filepath.Join(status.Dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
		old[name] = plugin.Hash(content)
	}
	writeManifest(t, status.Dir, old, "1.0.0", "auto")
	if got := integrations.OpenCode2Status(dir).State; got != integrations.StateOutdated {
		t.Fatalf("state = %q, want outdated", got)
	}
	upgraded, err := integrations.InstallOpenCode2(integrations.OpenCode2Status(dir), "1.1.0", "auto", false)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if upgraded.State != integrations.StateCurrent || upgraded.Version != "1.1.0" {
		t.Fatalf("after upgrade = %+v, want current at 1.1.0", upgraded)
	}
}

func TestStatusReportsModifiedAndRefusesOverwrite(t *testing.T) {
	dir := configDir(t)
	status := install(t, dir, "1.0.0", "auto")
	if err := os.WriteFile(filepath.Join(status.Dir, "policy.ts"), []byte("// edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modified := integrations.OpenCode2Status(dir)
	if modified.State != integrations.StateModified {
		t.Fatalf("state = %q, want modified", modified.State)
	}
	if _, err := integrations.InstallOpenCode2(modified, "1.1.0", "auto", false); err == nil {
		t.Fatal("install overwrote an edited file without --force")
	}
	forced, err := integrations.InstallOpenCode2(modified, "1.1.0", "auto", true)
	if err != nil {
		t.Fatalf("forced install: %v", err)
	}
	if forced.State != integrations.StateCurrent {
		t.Fatalf("state after --force = %q, want current", forced.State)
	}
}

// A copy someone installed by hand before another owned this file is byte for
// byte what another ships. Adopting it must not look like a conflict.
func TestHandCopiedInstallationIsAdoptable(t *testing.T) {
	dir := configDir(t)
	pluginDir := filepath.Join(dir, "plugins", plugin.DirName)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range plugin.Files() {
		if err := os.WriteFile(filepath.Join(pluginDir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	status := integrations.OpenCode2Status(dir)
	if status.State != integrations.StateAdoptable {
		t.Fatalf("state = %q, want adoptable", status.State)
	}
	adopted, err := integrations.InstallOpenCode2(status, "1.0.0", "en", false)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if adopted.State != integrations.StateCurrent {
		t.Fatalf("state = %q, want current", adopted.State)
	}
}

func TestForeignDirectoryIsNotOverwritten(t *testing.T) {
	dir := configDir(t)
	pluginDir := filepath.Join(dir, "plugins", plugin.DirName)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "index.ts"), []byte("export default {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := integrations.OpenCode2Status(dir)
	if status.State != integrations.StateForeign {
		t.Fatalf("state = %q, want foreign", status.State)
	}
	if _, err := integrations.InstallOpenCode2(status, "1.0.0", "auto", false); err == nil {
		t.Fatal("install overwrote a plugin another did not write")
	}
	if err := integrations.RemoveOpenCode2(status, false); err == nil {
		t.Fatal("remove deleted a plugin another did not write")
	}
}

// Changing the title language has to reach a plugin that is already loaded,
// which only happens if the files it watches are rewritten.
func TestLanguageChangeRewritesWatchedFiles(t *testing.T) {
	dir := configDir(t)
	status := install(t, dir, "1.0.0", "zh")
	entry := filepath.Join(status.Dir, "index.ts")
	before := modTime(t, entry)
	if _, err := integrations.InstallOpenCode2(status, "1.0.0", "zh", false); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if got := modTime(t, entry); got != before {
		t.Fatal("an unchanged installation rewrote the plugin and forced a reload")
	}
	switched, err := integrations.InstallOpenCode2(integrations.OpenCode2Status(dir), "1.0.0", "en", false)
	if err != nil {
		t.Fatalf("switch language: %v", err)
	}
	if got := modTime(t, entry); got == before {
		t.Fatal("a language change left the loaded plugin reading the old language")
	}
	if switched.Language != "en" {
		t.Fatalf("language = %q, want en", switched.Language)
	}
}

func TestRemoveTakesBackOnlyWhatAnotherWrote(t *testing.T) {
	dir := configDir(t)
	status := install(t, dir, "1.0.0", "auto")
	kept := filepath.Join(status.Dir, "notes.md")
	if err := os.WriteFile(kept, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := integrations.RemoveOpenCode2(status, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("remove deleted a file another did not write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(status.Dir, "index.ts")); !os.IsNotExist(err) {
		t.Fatal("remove left the plugin behind")
	}
	if err := os.Remove(kept); err != nil {
		t.Fatal(err)
	}
	status = install(t, dir, "1.0.0", "auto")
	if err := integrations.RemoveOpenCode2(status, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(status.Dir); !os.IsNotExist(err) {
		t.Fatal("remove left an empty plugin directory behind")
	}
}

// The explicit entry predates OpenCode 2 discovering the plugins directory. It
// is reported so it can be cleaned up, and the file is never edited.
func TestRedundantConfigEntryIsReportedNotEdited(t *testing.T) {
	dir := configDir(t)
	path := filepath.Join(dir, "opencode.jsonc")
	raw := "{\n  // kept\n  \"plugins\": [\"" + filepath.Join(dir, "plugins", plugin.DirName, "index.ts") + "\"]\n}\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	status := install(t, dir, "1.0.0", "auto")
	if status.RedundantEntry != path {
		t.Fatalf("redundant entry = %q, want %q", status.RedundantEntry, path)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != raw {
		t.Fatal("another edited the user's opencode.jsonc")
	}
}

func TestConfigDirEnvOverridesEverything(t *testing.T) {
	dir := configDir(t)
	t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "ignored"))
	if got := integrations.OpenCode2ConfigDir(context.Background()); got != dir {
		t.Fatalf("config dir = %q, want %q", got, dir)
	}
}

func writeManifest(t *testing.T, dir string, files map[string]string, version, language string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"integration":     integrations.OpenCode2,
		"plugin":          plugin.PluginID,
		"another_version": version,
		"title_language":  language,
		"files":           files,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".another-install.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func modTime(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime().UnixNano()
}
