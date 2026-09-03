package cli

import (
	"strings"
	"testing"
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
