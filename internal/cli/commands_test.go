package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/integrations"
	"github.com/nxxxsooo/another/internal/model"
)

// seededApp returns an app with two providers: alpha holds two sessions, one of
// them in a directory that no longer exists; beta is empty and installed, so it
// can receive migrations.
func seededApp(t *testing.T) (*App, *fakeProvider, *fakeProvider, string) {
	t.Helper()
	alpha, beta := newFake(t, "alpha"), newFake(t, "beta")
	project := t.TempDir()
	gone := filepath.Join(t.TempDir(), "renamed-away")
	alpha.seed(t, sampleConversation("alpha-one", project, "Stripe webhook retry"))
	notes := sampleConversation("alpha-two", gone, "Old project notes")
	notes.Messages = []model.Message{
		{Role: model.RoleUser, Content: "where did the wiki export land", Timestamp: seedCreated},
		{Role: model.RoleAssistant, Content: "under docs/legacy, next to the old runbook", Timestamp: seedCreated.Add(time.Minute)},
	}
	alpha.seed(t, notes)
	app := newTestApp(t, alpha, beta)
	return app, alpha, beta, gone
}

func TestListPrintsIndexedSessions(t *testing.T) {
	app, _, _, _ := seededApp(t)
	out := mustRun(t, app, "list")
	wantContains(t, out, "PROVIDER", "TITLE", "Stripe webhook retry", "Old project notes", "Fake alpha")

	out = mustRun(t, app, "list", "--json", "--provider", "alpha")
	var items []model.Summary
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}

	out = mustRun(t, app, "list", "--limit", "1", "--json")
	items = nil
	if err := json.Unmarshal([]byte(out), &items); err != nil || len(items) != 1 {
		t.Fatalf("limit: %d items, %v", len(items), err)
	}

	out = mustRun(t, app, "list", "--project", "renamed-away")
	if strings.Contains(out, "Stripe webhook retry") || !strings.Contains(out, "Old project notes") {
		t.Fatalf("project filter leaked:\n%s", out)
	}
}

func TestShowPrintsMessages(t *testing.T) {
	app, _, _, _ := seededApp(t)
	out := mustRun(t, app, "show", "alpha-one")
	wantContains(t, out, "Session alpha-one (alpha)", "Messages: 3", "[user]", "stripe webhook", "[assistant]")
	out = mustRun(t, app, "show", "alpha-two")
	wantContains(t, out, "Messages: 2", "wiki export")

	out = mustRun(t, app, "show", "alpha-one", "--limit", "1", "--raw")
	if strings.Contains(out, "stripe webhook") || !strings.Contains(out, "ship it") {
		t.Fatalf("--limit 1 should keep only the last message:\n%s", out)
	}

	_, err := run(t, app, "show", "nope")
	wantErr(t, err, "not found")
}

func TestSearchFindsMessageText(t *testing.T) {
	app, _, _, _ := seededApp(t)
	out := mustRun(t, app, "search", "idempotent", "--json")
	var res struct {
		Results []struct {
			Session model.Summary `json:"session"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(res.Results) != 1 || res.Results[0].Session.ID != "alpha-one" {
		t.Fatalf("results = %+v", res.Results)
	}
	out = mustRun(t, app, "search", "webhook", "--no-wait")
	wantContains(t, out, "TITLE / SNIPPET", "Stripe webhook retry")
}

func TestMigrateResumeAndDedup(t *testing.T) {
	app, _, beta, _ := seededApp(t)

	_, err := run(t, app, "resume", "alpha-one", "--to", "beta")
	wantErr(t, err, "not migrated to beta yet")

	_, err = run(t, app, "migrate", "alpha-one", "--to", "beta")
	wantErr(t, err, "confirmation requires a terminal")

	out := mustRun(t, app, "migrate", "alpha-two", "--to", "beta", "--dry-run")
	wantContains(t, out, "Dry run OK: would write to", "Context: auto")
	if beta.writes != 1 {
		t.Fatalf("dry run should reserve an id without writing a file; writes = %d", beta.writes)
	}
	if entries, _ := os.ReadDir(beta.root); len(entries) != 0 {
		t.Fatalf("dry run wrote %d files", len(entries))
	}

	out = mustRun(t, app, "migrate", "alpha-one", "--to", "beta", "--yes")
	wantContains(t, out, "✅ Migrated to Fake beta", "Resume:  beta --resume beta-w2", "3 source → 3 cleaned → 3 migrated")
	if entries, _ := os.ReadDir(beta.root); len(entries) != 1 {
		t.Fatalf("beta has %d files after migration", len(entries))
	}

	out = mustRun(t, app, "migrate", "alpha-one", "--to", "beta", "--yes")
	wantContains(t, out, "Already migrated to Fake beta")
	if entries, _ := os.ReadDir(beta.root); len(entries) != 1 {
		t.Fatalf("a second migration must not write again; beta has %d files", len(entries))
	}

	out = mustRun(t, app, "resume", "alpha-one", "--to", "beta")
	if strings.TrimSpace(out) != "beta --resume beta-w2" {
		t.Fatalf("resume = %q", out)
	}

	out = mustRun(t, app, "migrate", "alpha-one", "--to", "beta", "--dry-run")
	wantContains(t, out, "Dry run OK: already migrated to Fake beta")
}

func TestRootShortcutMigrates(t *testing.T) {
	app, _, beta, _ := seededApp(t)
	out := mustRun(t, app, "alpha-two", "--to", "beta", "--yes", "--context", "full")
	wantContains(t, out, "✅ Migrated to Fake beta", "Context: full")
	if entries, _ := os.ReadDir(beta.root); len(entries) != 1 {
		t.Fatalf("beta has %d files", len(entries))
	}
	_, err := run(t, app, "--migrate")
	wantErr(t, err, "guided migration requires a terminal")
}

func TestExportAndImportRoundTrip(t *testing.T) {
	app, _, beta, _ := seededApp(t)
	file := filepath.Join(t.TempDir(), "session.another.json")
	mustRun(t, app, "export", "alpha-one", "-o", file)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var conv model.Conversation
	if err := json.Unmarshal(data, &conv); err != nil || len(conv.Messages) != 3 {
		t.Fatalf("export: %v, %d messages", err, len(conv.Messages))
	}

	_, err = run(t, app, "import", file)
	wantErr(t, err, "--to is required")

	out := mustRun(t, app, "import", file, "--to", "beta")
	wantContains(t, out, "cancelled")

	out = mustRun(t, app, "import", file, "--to", "beta", "--dry-run")
	wantContains(t, out, "Dry run OK: would write to")

	out = mustRun(t, app, "import", file, "--to", "beta", "--yes")
	wantContains(t, out, "✅ Imported to Fake beta", "Resume:  beta --resume")
	if entries, _ := os.ReadDir(beta.root); len(entries) != 1 {
		t.Fatalf("beta has %d files", len(entries))
	}

	out = mustRun(t, app, "import", file, "--to", "beta", "--yes")
	wantContains(t, out, "Already imported to Fake beta")
	out = mustRun(t, app, "import", file, "--to", "beta", "--dry-run")
	wantContains(t, out, "Dry run OK: already exists at")

	_, err = run(t, app, "import", filepath.Join(t.TempDir(), "missing.json"), "--to", "beta", "--yes")
	if err == nil {
		t.Fatal("importing a missing file succeeded")
	}
}

func TestRenameWritesTheProviderAndRefreshesTheIndex(t *testing.T) {
	app, alpha, _, _ := seededApp(t)
	out := mustRun(t, app, "rename", "alpha-one", "--title", "0901|Fix|Stripe retries", "--dry-run")
	wantContains(t, out, `Dry run OK: would rename alpha-one to "0901|Fix|Stripe retries"`)
	if len(alpha.renamed) != 0 {
		t.Fatal("dry run renamed")
	}

	out = mustRun(t, app, "rename", "alpha-one", "--title", "0901|Fix|Stripe retries", "--from", "alpha")
	wantContains(t, out, "✅ Renamed alpha-one to 0901|Fix|Stripe retries")
	if alpha.renamed["alpha-one"] != "0901|Fix|Stripe retries" {
		t.Fatalf("provider title = %q", alpha.renamed["alpha-one"])
	}
	out = mustRun(t, app, "list", "--provider", "alpha")
	wantContains(t, out, "0901|Fix|Stripe retries")

	_, err := run(t, app, "rename", "alpha-one", "--auto")
	wantErr(t, err, "no title agent configured")

	t.Setenv("PI_SESSION_ID", "alpha-two")
	_, err = run(t, app, "rename", "alpha-two", "--title", "x")
	wantErr(t, err, "refusing to rename alpha-two")
}

func TestRenameAndRelocateRefuseProvidersWithoutTheContract(t *testing.T) {
	bare := newFake(t, "bare")
	bare.seed(t, sampleConversation("bare-one", t.TempDir(), "Plain"))
	app := newTestApp(t, bareProvider{f: bare})
	_, err := run(t, app, "rename", "bare-one", "--title", "x")
	wantErr(t, err, "Fake bare does not support rename")
	_, err = run(t, app, "relocate", "bare-one", "--to-dir", t.TempDir(), "--yes")
	wantErr(t, err, "Fake bare does not support relocating sessions")
}

func TestRelocateForksMovesAndDryRuns(t *testing.T) {
	app, alpha, _, _ := seededApp(t)
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	_, err = run(t, app, "relocate", "alpha-one", "--to-dir", target)
	wantErr(t, err, "confirmation requires a terminal")

	out := mustRun(t, app, "relocate", "alpha-one", "--to-dir", target, "--dry-run")
	wantContains(t, out, "Dry run OK: would fork alpha-one to "+target)
	if entries, _ := os.ReadDir(alpha.root); len(entries) != 2 {
		t.Fatalf("dry run changed the store: %d files", len(entries))
	}

	out = mustRun(t, app, "relocate", "alpha-one", "--to-dir", target, "--yes")
	wantContains(t, out, "✅ Forked alpha-one into "+target+" as alpha-one-fork", "Source session alpha-one is unchanged", "Resume: alpha --resume alpha-one-fork")
	if entries, _ := os.ReadDir(alpha.root); len(entries) != 3 {
		t.Fatalf("fork should add one file: %d", len(entries))
	}

	out = mustRun(t, app, "relocate", "alpha-two", "--to-dir", target, "--move", "--yes")
	wantContains(t, out, "✅ Moved alpha-two to "+target)
	conv, err := alpha.read("alpha-two")
	if err != nil || conv.ProjectPath != target {
		t.Fatalf("moved project = %q, %v", conv.ProjectPath, err)
	}
	out = mustRun(t, app, "paths")
	wantContains(t, out, "Every indexed directory still exists.")
}

func TestIndexStatusUpdateAndRebuild(t *testing.T) {
	app, _, _, _ := seededApp(t)
	out := mustRun(t, app, "index", "update")
	wantContains(t, out, "Updated 2 sessions", "Content indexed: 2 ready, 0 error")
	out = mustRun(t, app, "index", "status")
	wantContains(t, out, "Last update:", "Attribution rule:", "alpha: 2", "Content: ready=2 pending=0 error=0")
	out = mustRun(t, app, "index", "rebuild", "--metadata-only")
	wantContains(t, out, "Indexed 2 sessions")
	if strings.Contains(out, "Content indexed") {
		t.Fatalf("--metadata-only still indexed content:\n%s", out)
	}
	out = mustRun(t, app, "index", "rebuild", "--provider", "beta")
	wantContains(t, out, "Indexed 0 sessions")
}

func TestProvidersAndDoctor(t *testing.T) {
	app, alpha, _, _ := seededApp(t)
	out := mustRun(t, app, "providers")
	wantContains(t, out, "alpha", "data=ready", "cli=n/a", "sessions=2", "Fake alpha", "beta", "sessions=0")
	out = mustRun(t, app, "providers", "doctor")
	wantContains(t, out, "alpha", "sessions: "+alpha.root)
	if availability(false) != "missing" {
		t.Fatal("availability(false)")
	}
}

func TestPathsReportsLinksAndUnlinks(t *testing.T) {
	app, _, _, gone := seededApp(t)
	// paths reports on what is indexed and does not scan on its own.
	mustRun(t, app, "index", "update", "--metadata-only")
	out := mustRun(t, app, "paths")
	wantContains(t, out, "Directories that no longer exist: 1", gone, "1 session(s) from alpha")

	_, err := run(t, app, "paths", "link", gone, filepath.Join(t.TempDir(), "nope"))
	wantErr(t, err, "does not exist")
	_, err = run(t, app, "paths", "link", gone, gone)
	wantErr(t, err, "already where the sessions point")

	newHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out = mustRun(t, app, "paths", "link", gone, newHome)
	wantContains(t, out, "Linked", "1 session(s) now belong to")
	settings, err := config.LoadSettings()
	if err != nil || len(settings.PathAliases) != 1 || settings.PathAliases[0].To != newHome {
		t.Fatalf("saved aliases = %+v, %v", settings.PathAliases, err)
	}
	out = mustRun(t, app, "paths")
	wantContains(t, out, "Active aliases:", "Every indexed directory still exists.")

	out = mustRun(t, app, "list", "--project", filepath.Base(newHome))
	wantContains(t, out, "Old project notes")

	out = mustRun(t, app, "paths", "unlink", gone)
	wantContains(t, out, "Unlinked")
	_, err = run(t, app, "paths", "unlink", gone)
	wantErr(t, err, "no alias for")
	out = mustRun(t, app, "paths")
	wantContains(t, out, "Directories that no longer exist: 1")
}

func TestIntegrationsInstallStatusRemove(t *testing.T) {
	app := newTestApp(t, newFake(t, "alpha"))
	dir := t.TempDir()
	out := mustRun(t, app, "integrations", "status", "opencode2", "--config-dir", dir)
	wantContains(t, out, "not installed", "files:")

	out = mustRun(t, app, "integrations", "install", "opencode2", "--config-dir", dir)
	wantContains(t, out, "Installed", "installed (another dev)")
	settings, err := config.LoadSettings()
	if err != nil || !settings.Integrations.OpenCode2TitlePolicy {
		t.Fatalf("install did not record consent: %+v, %v", settings.Integrations, err)
	}
	out = mustRun(t, app, "integrations", "status", "opencode2", "--config-dir", dir)
	wantContains(t, out, "installed (another dev)")

	out = mustRun(t, app, "integrations", "remove", "opencode2", "--config-dir", dir)
	wantContains(t, out, "Removed")
	settings, _ = config.LoadSettings()
	if settings.Integrations.OpenCode2TitlePolicy {
		t.Fatal("remove left consent recorded")
	}
	out = mustRun(t, app, "integrations", "remove", "opencode2", "--config-dir", dir)
	wantContains(t, out, "Nothing installed at")
}

// Each adapter is installed, recorded, and removed on its own: consent to one
// agent's configuration directory is not consent to another's.
func TestIntegrationsTakeOneAdapterAtATime(t *testing.T) {
	app := newTestApp(t, newFake(t, "alpha"))
	dir := t.TempDir()
	t.Setenv(integrations.PiConfigDirEnv, dir)

	out := mustRun(t, app, "integrations", "install", "pi")
	wantContains(t, out, "Installed", "pi-title-policy")
	settings, err := config.LoadSettings()
	if err != nil || !settings.Integrations.PiTitlePolicy {
		t.Fatalf("install did not record consent: %+v, %v", settings.Integrations, err)
	}
	if settings.Integrations.OpenCode2TitlePolicy {
		t.Fatal("installing Pi's extension consented to OpenCode 2's directory")
	}
	if !integrations.PiStatus(dir).State.Installed() {
		t.Fatal("the extension was not written where Pi reads it")
	}

	out = mustRun(t, app, "integrations", "remove", "pi")
	wantContains(t, out, "Removed")
	settings, _ = config.LoadSettings()
	if settings.Integrations.PiTitlePolicy {
		t.Fatal("remove left consent recorded")
	}

	// A directory belongs to one agent, so it cannot be handed to a run that
	// covers every adapter.
	_, err = run(t, app, "integrations", "install", "--config-dir", dir)
	wantErr(t, err, "--config-dir needs one agent")
	_, err = run(t, app, "integrations", "status", "nope")
	wantErr(t, err, "invalid argument")
}

func TestApplyIntegrationsFollowsTheRecordedAnswer(t *testing.T) {
	newTestApp(t, newFake(t, "alpha"))
	dir := t.TempDir()
	resolvers := map[string]func() integrations.Status{
		integrations.OpenCode2: func() integrations.Status { return integrations.OpenCode2Status(dir) },
	}
	out := captureStdout(t, func() {
		applyIntegrations(config.Settings{}, resolvers)
	})
	if out != "" {
		t.Fatalf("no consent and nothing installed should print nothing, got %q", out)
	}
	consent := config.Settings{}
	consent.Integrations.OpenCode2TitlePolicy = true
	out = captureStdout(t, func() {
		applyIntegrations(consent, resolvers)
	})
	wantContains(t, out, "OpenCode 2 title plugin:")
	if !integrations.OpenCode2Status(dir).State.Installed() {
		t.Fatal("consent did not install the plugin")
	}
	out = captureStdout(t, func() {
		applyIntegrations(config.Settings{}, resolvers)
	})
	wantContains(t, out, "still installed; remove it with 'another integrations remove opencode2'")
	for _, state := range []integrations.State{integrations.StateCurrent, integrations.StateOutdated, integrations.StateModified, integrations.StateAdoptable, integrations.StateForeign, integrations.StateMissing} {
		if integrationStateText(integrations.Status{State: state, Version: "1.0"}) == "" {
			t.Fatalf("no text for %s", state)
		}
	}
	if versionText("") != "unknown version" {
		t.Fatal("versionText")
	}
}

func TestSetupRequiresATerminal(t *testing.T) {
	app := newTestApp(t, newFake(t, "alpha"))
	_, err := run(t, app, "setup")
	wantErr(t, err, "setup requires an interactive terminal")
	_, err = run(t, app, "migrate")
	wantErr(t, err, "guided migration requires a terminal")
	_, err = run(t, app, "migrate", "alpha-one")
	wantErr(t, err, "--to is required when stdin is not a terminal")
}
