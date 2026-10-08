package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
	"github.com/spf13/cobra"
)

func (a *App) relocateCmd() *cobra.Command {
	var from, toDir string
	var move, dryRun, yes, refresh, create bool
	cmd := &cobra.Command{
		Use:   "relocate <session-id>...",
		Short: "Fork or move sessions into another project directory",
		Long: "Fork or move one or more sessions into another project directory, using the\n" +
			"agent's own native operation. Fork is the default and leaves the source\n" +
			"session where it is; --move carries the session itself. --create makes the\n" +
			"target directory first, for a worktree that does not exist yet. This is not a\n" +
			"migration: the conversation is never re-rendered, so tool calls and\n" +
			"reasoning survive.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(toDir) == "" {
				return fmt.Errorf("--to-dir is required")
			}
			// A dry run writes nothing, so a missing target is reported as
			// the directory --create would make rather than made.
			directory, err := util.ResolveDir(toDir, create && !dryRun)
			if errors.Is(err, util.ErrDirMissing) && create && dryRun {
				expanded, expandErr := util.ExpandDir(toDir)
				if expandErr != nil {
					return expandErr
				}
				directory, err = util.NormalizeProjectPath(expanded), nil
				fmt.Printf("Dry run: would create %s\n", directory)
			}
			if errors.Is(err, util.ErrDirMissing) {
				return fmt.Errorf("%w (pass --create to make it)", err)
			}
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
			if !yes && !dryRun {
				if !stdinIsTerminal() {
					return fmt.Errorf("confirmation requires a terminal; pass --yes to relocate non-interactively")
				}
				prompt := fmt.Sprintf("%s %s to %s? [y/N] ", relocateAction(mode), relocateSubject(args), util.TildePath(directory))
				if !confirmAction(prompt) {
					fmt.Println("cancelled")
					return nil
				}
			}
			failed := 0
			for _, id := range args {
				if err := a.relocateOne(ctx, id, from, directory, mode, dryRun); err != nil {
					failed++
					if len(args) == 1 {
						return err
					}
					fmt.Printf("❌ %s: %s\n", id, err)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d sessions failed", failed, len(args))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&toDir, "to-dir", "", "target project directory (required)")
	cmd.Flags().StringVar(&from, "from", "", "source provider")
	cmd.Flags().BoolVar(&move, "move", false, "move the session instead of forking it")
	cmd.Flags().BoolVar(&create, "create", false, "create the target directory if it does not exist")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before relocate")
	return cmd
}

// relocateOne resolves and relocates a single session. Every refusal is an
// error so a multi-session call can report it on that session's line and
// carry on with the rest.
func (a *App) relocateOne(ctx context.Context, id, from, directory string, mode provider.RelocateMode, dryRun bool) error {
	sm, p, err := migrate.ResolveSession(ctx, a.Registry, a.Index, id, from)
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
	ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
	if dynamic, ok := p.(provider.SessionCapabilityProvider); ok {
		caps := dynamic.Capabilities(ref)
		if mode == provider.RelocateFork && !caps.RelocateFork || mode == provider.RelocateMove && !caps.RelocateMove {
			return fmt.Errorf("this %s session does not support %s", p.DisplayName(), relocateVerb(mode))
		}
	}
	res, err := relocator.RelocateSession(ctx, ref, provider.RelocateOpts{Directory: directory, Mode: mode, DryRun: dryRun})
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
	printRelocated(p, *sm, res, directory)
	return nil
}

func printRelocated(p provider.Provider, sm model.Summary, res *provider.RelocateResult, directory string) {
	if res.Moved {
		fmt.Printf("✅ Moved %s to %s\n", res.SessionID, directory)
	} else {
		fmt.Printf("✅ Forked %s into %s as %s\n", sm.ID, directory, res.SessionID)
		fmt.Printf("   Source session %s is unchanged\n", sm.ID)
	}
	fmt.Printf("   Resume: %s\n", p.ResumeCommand(provider.WriteResult{
		SessionID: res.SessionID, StoragePath: res.StoragePath, ProjectPath: res.ProjectPath,
	}))
}

func relocateSubject(ids []string) string {
	if len(ids) == 1 {
		return ids[0]
	}
	return fmt.Sprintf("%d sessions", len(ids))
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
