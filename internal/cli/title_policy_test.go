package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/config"
)

func TestTitlePolicyCommandEnablesAndRemovesBackground(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	oldValidate := validateDoubaoPolicy
	validateDoubaoPolicy = func(config.Settings) error { return nil }
	t.Cleanup(func() { validateDoubaoPolicy = oldValidate })
	settings := config.Settings{EnabledProviders: []string{"pi"}, TitleModel: &config.TitleModel{Provider: "codex", Model: "configured-model"}}
	if err := config.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	oldInstall, oldRemove := installDoubaoService, removeDoubaoService
	t.Cleanup(func() { installDoubaoService, removeDoubaoService = oldInstall, oldRemove })
	installs, removes := 0, 0
	installDoubaoService = func(executable string) error {
		installs++
		if !filepath.IsAbs(executable) {
			t.Fatalf("relative executable: %s", executable)
		}
		return nil
	}
	removeDoubaoService = func() error { removes++; return nil }
	run := func(args ...string) error { cmd := (&App{}).titlePolicyCmd(); cmd.SetArgs(args); return cmd.Execute() }
	if err := run("doubao", "--enable", "--background"); err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.TitlePolicy.DoubaoEvents || len(saved.EnabledProviders) != 2 || saved.EnabledProviders[1] != "doubao" || saved.TitleModel.Model != "configured-model" || installs != 1 {
		t.Fatalf("saved=%+v installs=%d", saved, installs)
	}
	saved.TitlePolicy.DoubaoEnabledAt = 1000
	if err := config.SaveSettings(saved); err != nil {
		t.Fatal(err)
	}
	if err := run("doubao", "--enable"); err != nil {
		t.Fatal(err)
	}
	saved, err = config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if saved.TitlePolicy.DoubaoEnabledAt != 1000 {
		t.Fatal("repeat enable reset cutoff")
	}
	if err := run("doubao", "--disable"); err != nil {
		t.Fatal(err)
	}
	saved, err = config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if saved.TitlePolicy.DoubaoEvents || removes != 1 {
		t.Fatalf("disabled=%t removes=%d", !saved.TitlePolicy.DoubaoEvents, removes)
	}
	if err := run("doubao", "--enable"); err != nil {
		t.Fatal(err)
	}
	saved, err = config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if saved.TitlePolicy.DoubaoEnabledAt <= 1000 {
		t.Fatal("re-enable did not establish a new cutoff")
	}
}

func TestTitlePolicyUnavailableSourceDoesNotEnableOrInstall(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveSettings(config.Settings{TitleModel: &config.TitleModel{Provider: "codex"}}); err != nil {
		t.Fatal(err)
	}
	oldValidate, oldInstall := validateDoubaoPolicy, installDoubaoService
	t.Cleanup(func() { validateDoubaoPolicy, installDoubaoService = oldValidate, oldInstall })
	validateDoubaoPolicy = func(config.Settings) error { return errors.New("event source unavailable") }
	installDoubaoService = func(string) error { t.Fatal("installed without source capability"); return nil }
	cmd := (&App{}).titlePolicyCmd()
	cmd.SetArgs([]string{"doubao", "--enable", "--background"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "event source unavailable") {
		t.Fatalf("error=%v", err)
	}
	saved, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if saved.TitlePolicy.DoubaoEvents || len(saved.EnabledProviders) > 0 {
		t.Fatal("failed validation changed consent")
	}
}

func TestTitlePolicyWatchRequiresConsentAndExclusiveFlags(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveSettings(config.Settings{}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"doubao", "--watch"}, {"doubao", "--watch", "--enable"}} {
		cmd := (&App{}).titlePolicyCmd()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestTitlePolicyMissingModelDoesNotInstall(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveSettings(config.Settings{}); err != nil {
		t.Fatal(err)
	}
	old := installDoubaoService
	t.Cleanup(func() { installDoubaoService = old })
	installDoubaoService = func(string) error { t.Fatal("service installed before model validation"); return nil }
	cmd := (&App{}).titlePolicyCmd()
	cmd.SetArgs([]string{"doubao", "--enable", "--background"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "no title agent configured") {
		t.Fatalf("error=%v", err)
	}
	saved, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if saved.TitlePolicy.DoubaoEvents || len(saved.EnabledProviders) > 0 {
		t.Fatal("failed enable changed settings")
	}
}

func TestMissingSettingsExcludeRemoteProviders(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	app, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Index.Close() }()
	for _, p := range app.Registry.All() {
		if remote, ok := p.(interface{ RequiresRemoteDiscovery() bool }); ok && remote.RequiresRemoteDiscovery() {
			t.Fatalf("unconfigured startup enables remote provider %s", p.ID())
		}
	}
}
