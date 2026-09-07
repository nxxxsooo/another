package cli

import (
	"fmt"
	"strings"

	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
	"github.com/spf13/cobra"
)

func (a *App) relocateCmd() *cobra.Command {
	var from, toDir string
	var move, dryRun, yes, refresh bool
	cmd := &cobra.Command{
		Use:   "relocate <session-id>",
		Short: "Fork or move a session into another project directory",
		Long: "Fork or move a session into another project directory, using the agent's own\n" +
			"native operation. Fork is the default and leaves the source session where it\n" +
			"is; --move carries the session itself. This is not a migration: the\n" +
			"conversation is never re-rendered, so tool calls and reasoning survive.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(toDir) == "" {
				return fmt.Errorf("--to-dir is required")
			}
			directory, err := util.ResolveExistingDir(toDir)
			if err != nil {
				return err
			}
			mode := provider.RelocateFork
			if move {
				mode = provider.RelocateMove
			}
			if err := a.ensureIndex(ctx, from, refresh); err != nil {
				return err
			}
			sm, p, err := migrate.ResolveSession(ctx, a.Registry, a.Index, args[0], from)
			if err != nil {
				return err
			}
			relocator, ok := p.(provider.SessionRelocator)
			if !ok {
				return fmt.Errorf("%s does not support relocating sessions", p.DisplayName())
			}
			if !relocator.SupportsRelocate(mode) {
				return fmt.Errorf("%s does not support %s", p.DisplayName(), relocateVerb(mode))
			}
			if !yes && !dryRun {
				if !stdinIsTerminal() {
					return fmt.Errorf("confirmation requires a terminal; pass --yes to relocate non-interactively")
				}
				prompt := fmt.Sprintf("%s %s to %s? [y/N] ", relocateAction(mode), sm.ID, util.TildePath(directory))
				if !confirmAction(prompt) {
					fmt.Println("cancelled")
					return nil
				}
			}
			res, err := relocator.RelocateSession(ctx, provider.SessionRef{
				ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
			}, provider.RelocateOpts{Directory: directory, Mode: mode, DryRun: dryRun})
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Printf("Dry run OK: would %s %s to %s\n", relocateVerbShort(mode), sm.ID, directory)
				return nil
			}
			if _, err := index.UpdateIncremental(ctx, a.Registry, a.Index, sm.Provider); err != nil {
				fmt.Printf("⚠️  Warning: relocated, but index refresh failed: %s\n", err)
			}
			if res.Moved {
				fmt.Printf("✅ Moved %s to %s\n", res.SessionID, directory)
			} else {
				fmt.Printf("✅ Forked %s into %s as %s\n", sm.ID, directory, res.SessionID)
				fmt.Printf("   Source session %s is unchanged\n", sm.ID)
			}
			fmt.Printf("   Resume: %s\n", p.ResumeCommand(provider.WriteResult{
				SessionID: res.SessionID, StoragePath: res.StoragePath, ProjectPath: res.ProjectPath,
			}))
			return nil
		},
	}
	cmd.Flags().StringVar(&toDir, "to-dir", "", "target project directory (required)")
	cmd.Flags().StringVar(&from, "from", "", "source provider")
	cmd.Flags().BoolVar(&move, "move", false, "move the session instead of forking it")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before relocate")
	return cmd
}

func relocateVerb(mode provider.RelocateMode) string {
	if mode == provider.RelocateMove {
		return "moving sessions to another directory"
	}
	return "forking sessions into another directory"
}

func relocateAction(mode provider.RelocateMode) string {
	if mode == provider.RelocateMove {
		return "Move"
	}
	return "Fork"
}

func relocateVerbShort(mode provider.RelocateMode) string {
	if mode == provider.RelocateMove {
		return "move"
	}
	return "fork"
}
