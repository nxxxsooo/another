package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
	"github.com/CyrusSE/agenthop/internal/tui"
	"github.com/CyrusSE/agenthop/internal/util"
	"github.com/charmbracelet/x/term"
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
	reg := registry.New()
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
	var to, from, project string
	var guided, dryRun, yes, refresh bool
	root := &cobra.Command{
		Use:           "agenthop",
		Short:         "Hop AI coding sessions between agents",
		Long:          "List, show, and migrate conversation sessions across Claude Code, Codex, Cursor, OpenCode, CommandCode, Hermes, and more.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
				return tui.RunMigrate(a.Registry, a.Index, a.Migrate, id, from)
			}
			if len(args) == 0 {
				return tui.Run(a.Registry, a.Index, a.Migrate)
			}
			if to == "" {
				return fmt.Errorf("--to is required for session %s (or use 'agenthop migrate %s' in a terminal)", args[0], args[0])
			}
			return a.runMigration(cmd, args[0], from, to, project, dryRun, yes, refresh)
		},
	}
	root.PersistentFlags().BoolVarP(&a.Verbose, "verbose", "v", false, "verbose output")
	root.Flags().BoolVar(&guided, "migrate", false, "open guided migration")
	root.Flags().StringVar(&to, "to", "", "target provider")
	root.Flags().StringVar(&from, "from", "", "source provider")
	root.Flags().StringVar(&project, "project", "", "target project path")
	root.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	root.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	root.Flags().BoolVar(&refresh, "refresh", false, "refresh index before migrate")

	root.AddCommand(a.listCmd())
	root.AddCommand(a.showCmd())
	root.AddCommand(a.migrateCmd())
	root.AddCommand(a.indexCmd())
	root.AddCommand(a.providersCmd())
	root.AddCommand(a.exportCmd())
	root.AddCommand(a.importCmd())
	root.AddCommand(a.resumeCmd())
	root.AddCommand(a.searchCmd())
	root.AddCommand(a.tuiCmd())
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
	return err
}

func (a *App) listCmd() *cobra.Command {
	var providerID, project string
	var limit int
	var asJSON, refresh, cwdOnly, includeSubagents bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List indexed sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.ensureIndex(ctx, providerID, refresh); err != nil {
				return err
			}
			opts := index.ListOpts{
				Provider:         registry.NormalizeID(providerID),
				Limit:            limit,
				IncludeSubagents: includeSubagents,
			}
			if cwdOnly {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				opts.ProjectCWD = util.NormalizeProjectPath(wd)
			} else if project != "" {
				opts.ProjectFilter = project
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
	cmd.Flags().StringVar(&project, "project", "", "filter by project path substring")
	cmd.Flags().BoolVar(&cwdOnly, "cwd", false, "only sessions for the current working directory")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (0 = unlimited)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before listing")
	cmd.Flags().BoolVar(&includeSubagents, "include-subagents", false, "include child agent sessions")
	return cmd
}

func (a *App) showCmd() *cobra.Command {
	var from string
	var limit int
	var raw, refresh bool
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
	return cmd
}

func (a *App) migrateCmd() *cobra.Command {
	var to, from, project string
	var dryRun, yes, refresh bool
	cmd := &cobra.Command{
		Use:   "migrate [session-id]",
		Short: "Migrate a session to another provider",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if !stdinIsTerminal() {
					return fmt.Errorf("guided migration requires a terminal")
				}
				return tui.RunMigrate(a.Registry, a.Index, a.Migrate, "", from)
			}
			if to == "" {
				if !stdinIsTerminal() {
					return fmt.Errorf("--to is required when stdin is not a terminal")
				}
				if err := a.ensureIndex(cmd.Context(), from, refresh); err != nil {
					return err
				}
				return tui.RunMigrate(a.Registry, a.Index, a.Migrate, args[0], from)
			}
			return a.runMigration(cmd, args[0], from, to, project, dryRun, yes, refresh)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "target provider")
	cmd.Flags().StringVar(&from, "from", "", "source provider")
	cmd.Flags().StringVar(&project, "project", "", "target project path")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before migrate")
	return cmd
}

func (a *App) runMigration(cmd *cobra.Command, id, from, to, project string, dryRun, yes, refresh bool) error {
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
		ProjectPath: project, DryRun: dryRun,
	})
	if err != nil {
		return err
	}
	if dryRun {
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
	for _, warning := range res.Warnings {
		fmt.Printf("⚠️  Warning: %s\n", warning)
	}
	return nil
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
	commands := map[string]string{
		"claude-code": "claude",
		"codex":       "codex",
		"cursor":      "cursor-agent",
		"opencode":    "opencode",
		"commandcode": "commandcode",
		"hermes":      "hermes",
	}
	name, ok := commands[id]
	if !ok {
		return "n/a"
	}
	if _, err := exec.LookPath(name); err == nil {
		return "ready"
	}
	return "missing"
}

func (a *App) exportCmd() *cobra.Command {
	var from, out string
	var refresh bool
	cmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export session to portable JSON",
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
			conv, err := p.Load(ctx, provider.SessionRef{ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath})
			if err != nil {
				return err
			}
			if out == "" {
				out = "session.agenthop.json"
			}
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			defer f.Close()
			enc := json.NewEncoder(f)
			enc.SetIndent("", "  ")
			return enc.Encode(conv)
		},
	}
	cmd.Flags().StringVar(&from, "provider", "", "source provider")
	cmd.Flags().StringVarP(&out, "output", "o", "", "output file")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before export")
	return cmd
}

func (a *App) tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.Run(a.Registry, a.Index, a.Migrate)
		},
	}
}

func (a *App) searchCmd() *cobra.Command {
	var providerID, project string
	var limit int
	var cwdOnly, includeSubagents, asJSON, noWait, refresh bool
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
			if cwdOnly {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				project = util.NormalizeProjectPath(wd)
			}
			searchOpts := index.SearchOpts{
				Query: strings.Join(args, " "), Provider: registry.NormalizeID(providerID),
				ProjectFilter: project, IncludeSubagents: includeSubagents, Limit: limit,
			}
			if cwdOnly {
				searchOpts.ProjectCWD, searchOpts.ProjectFilter = project, ""
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
	cmd.Flags().StringVar(&project, "project", "", "filter by project path substring")
	cmd.Flags().BoolVar(&cwdOnly, "cwd", false, "only sessions for the current working directory")
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
	fmt.Scanln(&answer)
	return strings.ToLower(strings.TrimSpace(answer)) == "y"
}
