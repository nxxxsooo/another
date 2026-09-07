package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/i18n"
)

func TestCommandSurface(t *testing.T) {
	a := &App{}
	root := a.Root()
	for _, flag := range []string{"migrate", "to", "from", "project", "context", "dry-run", "yes"} {
		if root.Flags().Lookup(flag) == nil {
			t.Errorf("missing root --%s", flag)
		}
	}
	setup, _, err := root.Find([]string{"setup"})
	if err != nil || setup == root {
		t.Fatalf("missing setup command: %v", err)
	}
	search, _, err := root.Find([]string{"search"})
	if err != nil {
		t.Fatal(err)
	}
	indexCmd, _, err := root.Find([]string{"index"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rebuild", "update"} {
		sub, _, err := indexCmd.Find([]string{name})
		if err != nil || sub.Flags().Lookup("metadata-only") == nil {
			t.Errorf("index %s missing --metadata-only", name)
		}
	}
	for _, flag := range []string{"provider", "cwd", "include-subagents", "limit", "json", "no-wait"} {
		if search.Flags().Lookup(flag) == nil {
			t.Errorf("missing search --%s", flag)
		}
	}
	migrate, _, err := root.Find([]string{"migrate"})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"abc"}} {
		if err := migrate.Args(migrate, args); err != nil {
			t.Errorf("migrate args %v: %v", args, err)
		}
	}
	if err := migrate.Args(migrate, []string{"a", "b"}); err == nil {
		t.Fatal("migrate accepted two session IDs")
	}
	if flag := migrate.Flags().Lookup("context"); flag == nil || flag.DefValue != "auto" {
		t.Fatalf("migrate --context default = %#v", flag)
	}
	importCmd, _, err := root.Find([]string{"import"})
	if err != nil {
		t.Fatal(err)
	}
	if flag := importCmd.Flags().Lookup("context"); flag == nil || flag.DefValue != "auto" {
		t.Fatalf("import --context default = %#v", flag)
	}
	tuiCmd, _, err := root.Find([]string{"tui"})
	if err != nil {
		t.Fatal(err)
	}
	if flag := tuiCmd.Flags().Lookup("context"); flag == nil || flag.DefValue != "auto" {
		t.Fatalf("tui --context default = %#v", flag)
	}
}

func TestProviderCLIStatusUnknown(t *testing.T) {
	if got := providerCLIStatus("future-provider"); got != "n/a" {
		t.Fatalf("status = %q", got)
	}
}

func TestRootSessionShortcutRequiresTarget(t *testing.T) {
	root := (&App{}).Root()
	root.SetArgs([]string{"abc123"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--to is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestMigrationRejectsInvalidContextBeforeUsingApp(t *testing.T) {
	root := (&App{}).Root()
	root.SetArgs([]string{"session", "--to", "codex", "--context", "everything", "--yes"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid context mode") {
		t.Fatalf("error = %v", err)
	}
}

// The interface language is resolved once, when the app is built, from the
// same configuration the rest of the run uses. If that wiring is missing the
// TUI still renders — in whatever language the package default happens to be —
// so nothing else in the program would notice.
func TestNewAppResolvesTheInterfaceLanguage(t *testing.T) {
	cases := []struct {
		name   string
		config string
		locale string
		want   i18n.Lang
	}{
		{"explicit chinese beats the locale", `{"version":1,"enabled_providers":["pi"],"ui":{"language":"zh"}}`, "en_US.UTF-8", i18n.LangChinese},
		{"explicit english beats the locale", `{"version":1,"enabled_providers":["pi"],"ui":{"language":"en"}}`, "zh_CN.UTF-8", i18n.LangEnglish},
		{"auto follows the locale", `{"version":1,"enabled_providers":["pi"],"ui":{"language":"auto"}}`, "zh_CN.UTF-8", i18n.LangChinese},
		{"a config without ui is auto", `{"version":1,"enabled_providers":["pi"]}`, "zh_CN.UTF-8", i18n.LangChinese},
		{"no config at all is auto", "", "zh_CN.UTF-8", i18n.LangChinese},
		{"an unreadable locale is English", `{"version":1,"enabled_providers":["pi"]}`, "", i18n.LangEnglish},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", root)
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("LC_ALL", tc.locale)
			t.Setenv("LC_MESSAGES", "")
			t.Setenv("LANG", "")
			if tc.config != "" {
				if err := os.MkdirAll(config.ConfigDir(), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config.SettingsPath(), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			i18n.SetCurrent(i18n.LangEnglish)
			app, err := NewApp()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { app.Index.Close() })
			if got := i18n.Current(); got != tc.want {
				t.Fatalf("interface language = %q, want %q", got, tc.want)
			}
		})
	}
}
