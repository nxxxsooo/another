package config_test

import (
	"os"
	"path/filepath"
	"reflect"
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
