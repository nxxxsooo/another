package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/integrations"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/recall"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/tui"
	"github.com/nxxxsooo/another/internal/util"
	"github.com/spf13/cobra"
)

var version = "dev"

type App struct {
	Registry *registry.Registry
	Index    *index.Store
	Migrate  *migrate.Engine
	Verbose  bool
}

func NewApp() (*App, error) {
	// version is stamped here by ldflags; the TUI borrows it for its goodbye
	// logo instead of carrying its own build metadata.
	tui.SetVersion(version)
	reg := registry.New()
	settings, settingsErr := config.LoadSettings()
	if settingsErr == nil {
		reg = registry.NewEnabled(settings.EnabledProviders)
	} else if !os.IsNotExist(settingsErr) {
		return nil, fmt.Errorf("load config: %w", settingsErr)
	} else {
		var local []string
		for _, p := range reg.All() {
			if remote, ok := p.(interface{ RequiresRemoteDiscovery() bool }); ok && remote.RequiresRemoteDiscovery() {
				continue
			}
			local = append(local, p.ID())
		}
		reg = registry.NewEnabled(local)
	}
	// Resolve the interface language before anything can draw. A missing
	// configuration is not a missing language: it means auto, which reads the
	// terminal's locale. The CLI's own help and errors stay English.
	tui.SetLanguage(settings.UI.Language)
	idx, err := index.Open("")
	if err != nil {
		return nil, err
	}
	return &App{
		Registry: reg,
		Index:    idx,
		Migrate:  &migrate.Engine{Registry: reg, Index: idx},
	}, nil
}

func (a *App) Root() *cobra.Command {
	var to, from, project, contextMode string
	var guided, dryRun, yes, refresh bool
	root := &cobra.Command{
		Use:           "another",
		Short:         "Every coding-agent session, in one place — and in any agent",
		Long:          "Find, resume, manage, @-mention, and migrate native sessions across Pi, Claude Code, Codex, OpenCode, Qwen Code, Antigravity, and more.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := migrate.ParseContextMode(contextMode)
			if err != nil {
				return err
			}
			if guided {
				if !stdinIsTerminal() {
					return fmt.Errorf("guided migration requires a terminal")
				}
				id := ""
				if len(args) == 1 {
					id = args[0]
					if err := a.ensureIndex(cmd.Context(), from, refresh); err != nil {
						return err
					}
				}
				return tui.RunMigrate(a.Registry, a.Index, a.Migrate, id, from, mode)
			}
			if len(args) == 0 {
				a.ensureAliases(cmd)
				if !config.SettingsExist() && stdinIsTerminal() {
					saved, err := a.runSetup(cmd.Context())
					if err != nil || !saved {
						return err
					}
				}
				return tui.Run(a.Registry, a.Index, a.Migrate, mode)
			}
			if to == "" {
				return fmt.Errorf("--to is required for session %s (or use 'another migrate %s' in a terminal)", args[0], args[0])
			}
			return a.runMigration(cmd, args[0], from, to, project, mode, dryRun, yes, refresh)
		},
	}
	root.PersistentFlags().BoolVarP(&a.Verbose, "verbose", "v", false, "verbose output")
	root.Flags().BoolVar(&guided, "migrate", false, "open guided migration")
	root.Flags().StringVar(&to, "to", "", "target provider")
	root.Flags().StringVar(&from, "from", "", "source provider")
	root.Flags().StringVar(&project, "project", "", "target project path")
	root.Flags().StringVar(&contextMode, "context", "auto", "context mode: auto, full, or recent")
	root.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	root.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	root.Flags().BoolVar(&refresh, "refresh", false, "refresh index before migrate")

	root.AddCommand(a.listCmd())
	root.AddCommand(a.showCmd())
	root.AddCommand(a.contextCmd())
	root.AddCommand(a.mcpCmd())
	root.AddCommand(a.migrateCmd())
	root.AddCommand(a.indexCmd())
	root.AddCommand(a.pathsCmd())
	root.AddCommand(a.providersCmd())
	root.AddCommand(a.setupCmd())
	root.AddCommand(a.integrationsCmd())
	root.AddCommand(a.exportCmd())
	root.AddCommand(a.importCmd())
	root.AddCommand(a.resumeCmd())
	root.AddCommand(a.relocateCmd())
	root.AddCommand(a.renameCmd())
	root.AddCommand(a.searchCmd())
	root.AddCommand(a.tuiCmd())
	root.AddCommand(a.updateCmd())
	root.AddCommand(a.aliasesCmd())
	root.AddCommand(a.titlePolicyCmd())
	return root
}

func (a *App) ensureIndex(ctx context.Context, providerFilter string, refresh bool) error {
	const indexMaxAge = 5 * time.Minute
	if !refresh {
		if providerFilter == "" {
			if !index.NeedsIncrementalIndex(a.Registry, a.Index, indexMaxAge) {
				return nil
			}
		} else {
			counts, _ := a.Index.CountByProvider()
			pid := registry.NormalizeID(providerFilter)
			if counts[pid] > 0 && !index.NeedsIncrementalIndex(a.Registry, a.Index, indexMaxAge) {
				return nil
			}
		}
	}
	_, err := index.UpdateIncremental(ctx, a.Registry, a.Index, registry.NormalizeID(providerFilter))
	return reportScanFailure(err)
}

// reportScanFailure lets a command run on what another could read. An agent
// whose store cannot be scanned is worth saying out loud, but it is not a
// reason to refuse a list that every other agent answered.
func reportScanFailure(err error) error {
	var failure *index.ScanFailure
	if errors.As(err, &failure) {
		fmt.Fprintf(os.Stderr, "another: skipped unreadable session store: %s\n", failure)
		return nil
	}
	return err
}

// currentScope is what the CLI reads as "here" unless told otherwise: the
// scope the browser opens on, so a search run in a repository and the list
// opened there agree. --all and --project both replace it.
func currentScope(ctx context.Context) (util.ProjectScope, error) {
	wd, err := os.Getwd()
	if err != nil {
		return util.ProjectScope{}, err
	}
	return util.DiscoverProjectScope(ctx, wd), nil
}

// scopeRoots resolves the project filter shared by list and search. A path
// substring searches every project for it; otherwise the current project
// applies unless --all lifts it.
func scopeRoots(ctx context.Context, project string, all bool) ([]string, error) {
	if project != "" || all {
		return nil, nil
	}
	scope, err := currentScope(ctx)
	if err != nil {
		return nil, err
	}
	return scope.Roots(), nil
}

// requireInScope refuses to read a session recorded outside the current
// project unless --all says so: an agent asking from one repository must not
// be handed another organization's conversation by default.
func requireInScope(ctx context.Context, sm *model.Summary, all bool) error {
	if all {
		return nil
	}
	scope, err := currentScope(ctx)
	if err != nil {
		return err
	}
	if len(scope.Roots()) == 0 || scope.Contains(sm.ProjectPath) {
		return nil
	}
	where := sm.ProjectPath
	if where == "" {
		where = "an unrecorded directory"
	}
	return fmt.Errorf("session %s belongs to %s, outside this project; pass --all to read it", sm.ID, where)
}

func (a *App) listCmd() *cobra.Command {
	var providerID, project string
	var limit int
	var asJSON, refresh, all, includeSubagents bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List indexed sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.ensureIndex(ctx, providerID, refresh); err != nil {
				return err
			}
			roots, err := scopeRoots(ctx, project, all)
			if err != nil {
				return err
			}
			opts := index.ListOpts{
				Provider:         registry.NormalizeID(providerID),
				ProjectFilter:    project,
				ProjectRoots:     roots,
				Limit:            limit,
				IncludeSubagents: includeSubagents,
			}
			items, err := a.Index.List(opts)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(items)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPROVIDER\tUPDATED\tMSGS\tTITLE")
			for _, s := range items {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
					s.ShortID(), registry.DisplayName(a.Registry, s.Provider),
					util.FormatRelative(s.UpdatedAt), s.MessageCount, util.TruncateRunes(s.Title, 50))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&providerID, "provider", "", "filter by provider")
	cmd.Flags().StringVar(&project, "project", "", "filter every project by path substring")
	cmd.Flags().BoolVar(&all, "all", false, "include every project, not only the current one")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (0 = unlimited)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before listing")
	cmd.Flags().BoolVar(&includeSubagents, "include-subagents", false, "include child agent sessions")
	return cmd
}

func (a *App) showCmd() *cobra.Command {
	var from string
	var limit int
	var raw, refresh, all bool
	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show session messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.ensureIndex(ctx, from, refresh); err != nil {
				return err
			}
			sm, p, err := migrate.ResolveSession(ctx, a.Registry, a.Index, args[0], from)
			if err != nil {
				return err
			}
			if err := requireInScope(ctx, sm, all); err != nil {
				return err
			}
			ref := provider.SessionRef{
				ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
			}
			var conv *model.Conversation
			if preview, ok := p.(provider.PreviewLoader); ok && limit > 0 {
				conv, err = preview.LoadPreview(ctx, ref, limit)
			} else {
				conv, err = p.Load(ctx, ref)
			}
			if err != nil {
				return err
			}
			fmt.Printf("Session %s (%s)\nProject: %s\nMessages: %d\n\n", conv.ID, conv.Provider, conv.ProjectPath, len(conv.Messages))
			msgs := conv.Messages
			if limit > 0 && len(msgs) > limit {
				msgs = msgs[len(msgs)-limit:]
			}
			for _, m := range msgs {
				text := m.PlainText()
				if m.Role == model.RoleUser {
					text = util.DisplayUserText(text)
				}
				if !raw {
					text = util.TruncateRunes(text, 2000)
				}
				fmt.Printf("[%s]\n%s\n\n", m.Role, text)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "provider", "", "source provider")
	cmd.Flags().IntVar(&limit, "limit", 0, "show last N messages")
	cmd.Flags().BoolVar(&raw, "raw", false, "do not truncate")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before show")
	cmd.Flags().BoolVar(&all, "all", false, "allow a session from any project, not only the current one")
	return cmd
}

func (a *App) migrateCmd() *cobra.Command {
	var to, from, project, contextMode string
	var dryRun, yes, refresh bool
	cmd := &cobra.Command{
		Use:   "migrate [session-id]",
		Short: "Migrate a session to another provider",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := migrate.ParseContextMode(contextMode)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if !stdinIsTerminal() {
					return fmt.Errorf("guided migration requires a terminal")
				}
				return tui.RunMigrate(a.Registry, a.Index, a.Migrate, "", from, mode)
			}
			if to == "" {
				if !stdinIsTerminal() {
					return fmt.Errorf("--to is required when stdin is not a terminal")
				}
				if err := a.ensureIndex(cmd.Context(), from, refresh); err != nil {
					return err
				}
				return tui.RunMigrate(a.Registry, a.Index, a.Migrate, args[0], from, mode)
			}
			return a.runMigration(cmd, args[0], from, to, project, mode, dryRun, yes, refresh)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "target provider")
	cmd.Flags().StringVar(&from, "from", "", "source provider")
	cmd.Flags().StringVar(&project, "project", "", "target project path")
	cmd.Flags().StringVar(&contextMode, "context", "auto", "context mode: auto, full, or recent")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before migrate")
	return cmd
}

func (a *App) runMigration(cmd *cobra.Command, id, from, to, project string, contextMode migrate.ContextMode, dryRun, yes, refresh bool) error {
	ctx := cmd.Context()
	if err := a.ensureIndex(ctx, from, refresh); err != nil {
		return err
	}
	if !yes && !dryRun {
		if !stdinIsTerminal() {
			return fmt.Errorf("confirmation requires a terminal; pass --yes to migrate non-interactively")
		}
		if !confirmAction(fmt.Sprintf("Migrate %s → %s? [y/N] ", id, to)) {
			fmt.Println("cancelled")
			return nil
		}
	}
	res, err := a.Migrate.Run(ctx, migrate.Options{
		SessionID: id, FromProvider: from, ToProvider: to,
		ProjectPath: project, DryRun: dryRun, ContextMode: contextMode,
	})
	if err != nil {
		return err
	}
	if dryRun {
		printProjectionSummary(res)
		if res.AlreadyExists {
			fmt.Printf("Dry run OK: already migrated to %s\n   Path: %s\n", res.TargetName, res.Write.StoragePath)
		} else {
			fmt.Printf("Dry run OK: would write to %s\n", res.Write.StoragePath)
		}
		for _, warning := range res.Warnings {
			fmt.Printf("⚠️  Warning: %s\n", warning)
		}
		return nil
	}
	if res.AlreadyExists {
		fmt.Printf("ℹ️  Already migrated to %s\n", res.TargetName)
	} else {
		fmt.Printf("✅ Migrated to %s\n", res.TargetName)
	}
	fmt.Printf("   Session: %s\n", res.Write.SessionID)
	fmt.Printf("   Path:    %s\n", res.Write.StoragePath)
	fmt.Printf("   Resume:  %s\n", res.Resume)
	printProjectionSummary(res)
	for _, warning := range res.Warnings {
		fmt.Printf("⚠️  Warning: %s\n", warning)
	}
	return nil
}

func printProjectionSummary(res *migrate.Result) {
	fmt.Printf("   Context: %s (%d source → %d cleaned → %d migrated)\n", res.ContextMode, res.SourceCount, res.CleanedCount, res.ProjectedCount)
}

func (a *App) indexCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "index", Short: "Manage session index"}
	var providerID string
	status := &cobra.Command{
		Use:   "status",
		Short: "Show index status",
		RunE: func(cmd *cobra.Command, args []string) error {
			counts, err := a.Index.CountByProvider()
			if err != nil {
				return err
			}
			last, _ := a.Index.GetMeta("last_update")
			rebuild, _ := a.Index.GetMeta("last_rebuild")
			fmt.Printf("Last update: %s\n", last)
			fmt.Printf("Last rebuild: %s\n", rebuild)
			fmt.Printf("Attribution rule: %s\n", index.AttributionRule)
			ids := make([]string, 0, len(counts))
			for id := range counts {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				fmt.Printf("  %s: %d\n", id, counts[id])
			}
			content, err := a.Index.ContentStatus()
			if err != nil {
				return err
			}
			fmt.Printf("Content: ready=%d pending=%d error=%d\n", content.Indexed, content.Pending, content.Failed)
			return nil
		},
	}
	var rebuildMetadataOnly bool
	rebuild := &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild session index",
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := index.Rebuild(cmd.Context(), a.Registry, a.Index, registry.NormalizeID(providerID))
			if err != nil {
				return err
			}
			fmt.Printf("Indexed %d sessions\n", n)
			return a.finishContentIndex(cmd.Context(), rebuildMetadataOnly)
		},
	}
	rebuild.Flags().StringVar(&providerID, "provider", "", "single provider")
	rebuild.Flags().BoolVar(&rebuildMetadataOnly, "metadata-only", false, "skip full-text content indexing")
	var updateMetadataOnly bool
	update := &cobra.Command{
		Use:   "update",
		Short: "Incremental index update",
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := index.UpdateIncremental(cmd.Context(), a.Registry, a.Index, registry.NormalizeID(providerID))
			if err != nil {
				return err
			}
			fmt.Printf("Updated %d sessions\n", n)
			return a.finishContentIndex(cmd.Context(), updateMetadataOnly)
		},
	}
	update.Flags().StringVar(&providerID, "provider", "", "single provider")
	update.Flags().BoolVar(&updateMetadataOnly, "metadata-only", false, "skip full-text content indexing")
	cmd.AddCommand(status, rebuild, update)
	return cmd
}

func (a *App) finishContentIndex(ctx context.Context, metadataOnly bool) error {
	if metadataOnly {
		return nil
	}
	indexed, failed, err := a.Index.IndexPendingContent(ctx, a.Registry, 0, false)
	if err != nil {
		return err
	}
	fmt.Printf("Content indexed: %d ready, %d error\n", indexed, failed)
	return nil
}

func (a *App) setupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Choose which coding agents another manages",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stdinIsTerminal() {
				return fmt.Errorf("setup requires an interactive terminal")
			}
			_, err := a.runSetup(cmd.Context())
			return err
		},
	}
}

func (a *App) runSetup(ctx context.Context) (bool, error) {
	counts, _ := a.Index.CountByProvider()
	var initial config.Settings
	if settings, err := config.LoadSettings(); err == nil {
		initial = settings
		if initial.TitleModel != nil && initial.TitleModel.Language == "" {
			initial.TitleModel.Language = settings.TitlePolicy.Language
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("load config: %w", err)
	}
	all := registry.NewOrdered(initial.EnabledProviders)
	// Finding OpenCode 2's adapter means asking it where its configuration is,
	// which can start a stopped service. Setup must not wait for that before
	// it draws, so each lookup runs once, in the background, and is reused
	// afterwards by whatever setup decides to do with it.
	resolvers := make(map[string]func() integrations.Status, len(integrations.Adapters()))
	plugins := make([]tui.SetupPlugin, 0, len(integrations.Adapters()))
	for _, adapter := range integrations.Adapters() {
		var once sync.Once
		var found integrations.Status
		resolvers[adapter.ID] = func() integrations.Status {
			once.Do(func() { found = adapter.Status(adapter.ConfigDir(ctx)) })
			return found
		}
		// Whether a row exists at all is cheap to answer and does not wait.
		plugins = append(plugins, tui.SetupPlugin{
			Integration: adapter.ID,
			Provider:    adapter.Provider,
			Supported:   registry.CLIAvailable(adapter.Provider),
		})
	}
	probe := func(id string) tui.SetupPlugin {
		row := tui.SetupPlugin{Integration: id}
		for _, p := range plugins {
			if p.Integration == id {
				row = p
			}
		}
		resolve, ok := resolvers[id]
		if !ok {
			return row
		}
		status := resolve()
		row.Supported = row.Supported || status.State.Installed()
		row.Dir, row.State = status.Dir, status.State
		return row
	}
	count := sessionCounter(ctx, all)
	countAllowed := func(id string) (int, error) {
		p, err := all.Get(id)
		if err != nil {
			return 0, err
		}
		if remote, ok := p.(interface{ RequiresRemoteDiscovery() bool }); ok && remote.RequiresRemoteDiscovery() {
			enabled := false
			for _, selected := range initial.EnabledProviders {
				if selected == id {
					enabled = true
				}
			}
			if !enabled {
				return counts[id], nil
			}
		}
		return count(id)
	}
	settings, done, err := tui.RunSetup(all, counts, initial, plugins, probe, countAllowed)
	if err != nil || !done {
		return false, err
	}
	if err := config.SaveSettings(settings); err != nil {
		return false, fmt.Errorf("save config: %w", err)
	}
	applyIntegrations(settings, resolvers)
	enabled := settings.EnabledProviders
	if err := a.Index.KeepProviders(enabled); err != nil {
		return false, fmt.Errorf("prune index: %w", err)
	}
	a.Registry = registry.NewEnabled(enabled)
	a.Migrate = &migrate.Engine{Registry: a.Registry, Index: a.Index}
	if _, err := index.UpdateIncremental(ctx, a.Registry, a.Index, ""); reportScanFailure(err) != nil {
		return false, fmt.Errorf("index selected agents: %w", err)
	}
	fmt.Printf("Configured %d agents: %s\n", len(enabled), strings.Join(enabled, ", "))
	return true, nil
}

// sessionCounter reads one agent's session count off its own storage, which is
// where setup's numbers have to come from on a first run: the index is empty
// until agents have been chosen, and "0 sessions" next to an installed CLI is
// not a report, it is a wrong answer to the question the page is asking.
// Nothing is written; this is the same scan the index would perform, minus the
// title-generation leftovers another created itself.
func sessionCounter(ctx context.Context, reg *registry.Registry) func(string) (int, error) {
	return func(id string) (int, error) {
		p, err := reg.Get(id)
		if err != nil {
			return 0, err
		}
		if !p.Installed() {
			return 0, nil
		}
		summaries, err := p.Discover(ctx, provider.DiscoverOpts{})
		if err != nil {
			return 0, err
		}
		seen := make(map[string]struct{}, len(summaries))
		for _, sm := range index.DiscoverIndexable(ctx, p, summaries) {
			seen[sm.ID] = struct{}{}
		}
		return len(seen), nil
	}
}

func (a *App) providersCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "providers", Short: "List providers"}
	doctor := &cobra.Command{
		Use:   "doctor",
		Short: "Check provider paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, p := range a.Registry.All() {
				fmt.Printf("%-14s data=%-7s cli=%-7s %s\n",
					p.ID(), availability(p.Installed()), providerCLIStatus(p.ID()), p.DisplayName())
				for _, ps := range p.DefaultPaths() {
					fmt.Printf("    %s: %s\n", ps.Label, util.TildePath(ps.Path))
				}
				if adapter, ok := integrations.ForProvider(p.ID()); ok {
					// The adapter is part of whether that agent is set up
					// correctly, and it lives outside every path above.
					found := adapter.Status(adapter.ConfigDir(cmd.Context()))
					fmt.Printf("    title adapter: %s\n", integrationStateText(found))
					if found.RedundantEntry != "" {
						fmt.Printf("    note: %s still lists this plugin; OpenCode finds it without that entry\n",
							util.TildePath(found.RedundantEntry))
					}
				}
			}
			return nil
		},
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if index.NeedsIncrementalIndex(a.Registry, a.Index, 5*time.Minute) {
			_, _ = index.UpdateIncremental(cmd.Context(), a.Registry, a.Index, "")
		}
		counts, _ := a.Index.CountByProvider()
		for _, p := range a.Registry.All() {
			fmt.Printf("%-14s data=%-7s cli=%-7s sessions=%d  %s\n",
				p.ID(), availability(p.Installed()), providerCLIStatus(p.ID()), counts[p.ID()], p.DisplayName())
		}
		return nil
	}
	cmd.AddCommand(doctor)
	return cmd
}

func availability(ok bool) string {
	if ok {
		return "ready"
	}
	return "missing"
}

func providerCLIStatus(id string) string {
	if registry.CLICommand(id) == "" {
		return "n/a"
	}
	if registry.CLIAvailable(id) {
		return "ready"
	}
	return "missing"
}

func (a *App) exportCmd() *cobra.Command {
	var from, out, format string
	var refresh bool
	cmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export a session as portable JSON or a readable Markdown transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if format != "json" && format != "markdown" {
				return fmt.Errorf("unknown format %q: use json or markdown", format)
			}
			if err := a.ensureIndex(ctx, from, refresh); err != nil {
				return err
			}
			sm, p, err := migrate.ResolveSession(ctx, a.Registry, a.Index, args[0], from)
			if err != nil {
				return err
			}
			conv, err := p.Load(ctx, provider.SessionRef{ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath})
			if err != nil {
				return err
			}
			if out == "" {
				out = "session.another.json"
				if format == "markdown" {
					out = "session.another.md"
				}
			}
			w := io.Writer(os.Stdout)
			if out != "-" {
				f, err := os.Create(out)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				w = f
			}
			if format == "markdown" {
				_, err := io.WriteString(w, recall.Render(conv, recall.Source{AgentName: registry.DisplayName(a.Registry, sm.Provider)}, recall.Full))
				return err
			}
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(conv)
		},
	}
	cmd.Flags().StringVar(&from, "provider", "", "source provider")
	cmd.Flags().StringVarP(&out, "output", "o", "", "output file, or - for stdout")
	cmd.Flags().StringVar(&format, "format", "json", "json (portable, re-importable) or markdown (readable transcript)")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before export")
	return cmd
}

func (a *App) tuiCmd() *cobra.Command {
	var contextMode, from string
	var refresh bool
	cmd := &cobra.Command{
		Use:   "tui [session-id]",
		Short: "Interactive terminal UI; with a session ID, opens on that session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := migrate.ParseContextMode(contextMode)
			if err != nil {
				return err
			}
			a.ensureAliases(cmd)
			if len(args) == 1 {
				if !stdinIsTerminal() {
					return fmt.Errorf("opening a session requires a terminal")
				}
				if err := a.ensureIndex(cmd.Context(), from, refresh); err != nil {
					return err
				}
				return tui.RunFocused(a.Registry, a.Index, a.Migrate, args[0], from, mode)
			}
			return tui.Run(a.Registry, a.Index, a.Migrate, mode)
		},
	}
	cmd.Flags().StringVar(&contextMode, "context", "auto", "context mode: auto, full, or recent")
	cmd.Flags().StringVar(&from, "provider", "", "source provider of the session")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before lookup")
	return cmd
}

func (a *App) searchCmd() *cobra.Command {
	var providerID, project string
	var limit int
	var all, includeSubagents, asJSON, noWait, refresh bool
	cmd := &cobra.Command{
		Use:   "search <keywords>",
		Short: "Search session titles and message text",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.ensureIndex(ctx, providerID, refresh); err != nil {
				return err
			}
			if !noWait {
				if _, _, err := a.Index.IndexPendingContent(ctx, a.Registry, 0, false); err != nil {
					return err
				}
			}
			roots, err := scopeRoots(ctx, project, all)
			if err != nil {
				return err
			}
			searchOpts := index.SearchOpts{
				Query: strings.Join(args, " "), Provider: registry.NormalizeID(providerID),
				ProjectFilter: project, ProjectRoots: roots, IncludeSubagents: includeSubagents, Limit: limit,
			}
			hits, err := a.Index.Search(searchOpts)
			if err != nil {
				return err
			}
			status, err := a.Index.ContentStatus()
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(struct {
					Results []index.SearchHit        `json:"results"`
					Content index.ContentIndexStatus `json:"content_index"`
				}{hits, status})
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPROVIDER\tUPDATED\tMATCH\tTITLE / SNIPPET")
			for _, hit := range hits {
				detail := strings.TrimSpace(hit.Snippet)
				if detail == "" || detail == hit.Session.Title {
					detail = hit.Session.Title
				} else {
					detail = hit.Session.Title + " — " + strings.ReplaceAll(detail, "\n", " ")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", hit.Session.ShortID(),
					registry.DisplayName(a.Registry, hit.Session.Provider), util.FormatRelative(hit.Session.UpdatedAt),
					hit.MatchType, util.TruncateRunes(detail, 100))
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if status.Pending > 0 || status.Failed > 0 {
				fmt.Fprintf(os.Stderr, "content index: %d ready, %d pending, %d error\n", status.Indexed, status.Pending, status.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&providerID, "provider", "", "filter by provider")
	cmd.Flags().StringVar(&project, "project", "", "filter every project by path substring")
	cmd.Flags().BoolVar(&all, "all", false, "search every project, not only the current one")
	cmd.Flags().BoolVar(&includeSubagents, "include-subagents", false, "include child agent sessions")
	cmd.Flags().IntVar(&limit, "limit", 50, "max results")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return available results without waiting for content indexing")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before searching")
	return cmd
}

func stdinIsTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd())
}

func confirmAction(prompt string) bool {
	fmt.Print(prompt)
	var answer string
	_, _ = fmt.Scanln(&answer)
	return strings.ToLower(strings.TrimSpace(answer)) == "y"
}
