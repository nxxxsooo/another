package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/config"
)

func TestSettingsRoundTripAndPermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	want := config.Settings{EnabledProviders: []string{"pi", "codex", "opencode2"}}
	if err := config.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != config.SettingsVersion || !reflect.DeepEqual(got.EnabledProviders, want.EnabledProviders) {
		t.Fatalf("settings = %+v", got)
	}
	info, err := os.Stat(config.SettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(config.SettingsPath()))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("config directory mode = %o, want 700", dir.Mode().Perm())
	}
}

func TestLoadSettingsRejectsUnknownVersion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(config.ConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.SettingsPath(), []byte(`{"version":99,"enabled_providers":["pi"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadSettings(); err == nil {
		t.Fatal("unknown config version was accepted")
	}
}

func TestTitlePolicyAndModelLanguageStayInSync(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	want := config.Settings{
		EnabledProviders: []string{"pi"},
		TitleModel:       &config.TitleModel{Provider: "pi", Language: "en"},
	}
	if err := config.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.TitlePolicy.Language != "en" || got.TitleModel == nil || got.TitleModel.Language != "en" {
		t.Fatalf("language representations drifted: %+v", got)
	}
}

// The interface language is a separate setting from the title language, and a
// round trip has to keep them apart rather than collapsing one into the other.
func TestUILanguageIsStoredSeparatelyFromTitleLanguage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	want := config.Settings{
		EnabledProviders: []string{"pi"},
		TitlePolicy:      config.TitlePolicy{Language: "auto"},
		UI:               config.UI{Language: "en"},
	}
	if err := config.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.UI.Language != "en" {
		t.Fatalf("ui language = %q, want en", got.UI.Language)
	}
	if got.TitlePolicy.Language != "auto" {
		t.Fatalf("title language followed the interface language: %+v", got)
	}
}

// A configuration written by an older build has no ui key. It must load
// cleanly and leave the interface on auto rather than failing the version
// check or inventing a language.
func TestConfigWithoutUISectionLoadsAsAuto(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(config.ConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":1,"enabled_providers":["pi"],"title_policy":{"language":"zh"}}`
	if err := os.WriteFile(config.SettingsPath(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.UI.Language != "" {
		t.Fatalf("missing ui section produced %q instead of an empty preference", got.UI.Language)
	}
}

func TestLegacyTitlePolicyMigratesIntoModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(config.ConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":1,"enabled_providers":["agy"],"title_model":{"provider":"agy"},"title_policy":{"language":"auto"}}`
	if err := os.WriteFile(config.SettingsPath(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleModel == nil || got.TitleModel.Language != "auto" {
		t.Fatalf("shared policy did not migrate into title model: %+v", got)
	}
}

// A config written before path aliases existed must keep loading, and saving
// one back must not invent a field the person never set.
func TestSettingsWithoutPathAliasesStillLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "another", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"enabled_providers":["codex"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.PathAliases) != 0 {
		t.Fatalf("PathAliases = %+v, want none", settings.PathAliases)
	}
	if err := config.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "path_aliases") {
		t.Fatalf("saved config invented a field: %s", data)
	}
}

func TestPathAliasesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	want := []config.PathAlias{{From: "/old/Projects/fit", To: "/new/Work/fit/projects"}}
	if err := config.SaveSettings(config.Settings{Version: config.SettingsVersion, PathAliases: want}); err != nil {
		t.Fatal(err)
	}
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.PathAliases) != 1 || settings.PathAliases[0] != want[0] {
		t.Fatalf("PathAliases = %+v, want %+v", settings.PathAliases, want)
	}
}
