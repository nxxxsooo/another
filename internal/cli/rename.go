package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/titler"
	"github.com/spf13/cobra"
)

func (a *App) renameCmd() *cobra.Command {
	var from, title string
	var auto, dryRun, refresh, allowCurrent, skipConforming bool
	cmd := &cobra.Command{
		Use:   "rename <session-id>",
		Short: "Rename a session in its agent's own title store",
		Long: "Rename a session in the agent's native title store — the same write Ctrl+R\n" +
			"performs in the TUI. --title sets the text verbatim; --auto asks the\n" +
			"configured title agent and applies the MMDD|Type|Topic policy.\n\n" +
			"This is the non-interactive form a SessionEnd hook can call. another keeps\n" +
			"no private display aliases, so the new name is the one the agent itself\n" +
			"shows on its next launch.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			title = strings.TrimSpace(title)
			if (title != "") == auto {
				return fmt.Errorf("pass exactly one of --title or --auto")
			}
			if err := a.ensureIndex(ctx, from, refresh); err != nil {
				return err
			}
			sm, p, err := migrate.ResolveSession(ctx, a.Registry, a.Index, args[0], from)
			if err != nil {
				return err
			}
			renamer, ok := p.(provider.SessionRenamer)
			if !ok {
				return fmt.Errorf("%s does not support rename", p.DisplayName())
			}
			// Renaming the live session races the agent that owns it. The one
			// place that is not true is a SessionEnd hook: the session is over,
			// there is no next turn to lose, and the agent still exports its id
			// to the hook's environment — so the refusal has to be overridable
			// by a caller that can name that situation.
			if provider.IsCurrentSession(*sm) && !allowCurrent {
				return fmt.Errorf("refusing to rename %s: it is the session this process is running in (pass --allow-current from a SessionEnd hook)", sm.ID)
			}
			// A title that already follows the policy is left where it is.
			// The check belongs to another rather than to the caller: an
			// extension matching the format itself would be a second policy,
			// and the one place a trigger could read the current title —
			// the agent's own in-memory session name — cannot see the title
			// another wrote straight into the session store.
			if auto && skipConforming {
				cfg, err := titleConfig()
				if err != nil {
					return err
				}
				if titler.Conforms(cfg.Language, sm.Title) {
					fmt.Printf("Unchanged: %s already follows the policy (%s)\n", sm.ID, sm.Title)
					return nil
				}
			}
			if auto {
				suggested, err := a.suggestTitle(ctx, p, *sm)
				if err != nil {
					return err
				}
				title = suggested
			}
			if dryRun {
				fmt.Printf("Dry run OK: would rename %s to %q\n", sm.ID, title)
				return nil
			}
			// ErrPartial is a caveat, not a failure: the agent's own store has
			// the new title and some surface it also reads does not. Reporting
			// failure would send the caller to redo a rename that happened.
			var caveat error
			if err := renamer.RenameSession(ctx, provider.SessionRef{
				ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
			}, title); err != nil {
				if !errors.Is(err, provider.ErrPartial) {
					return err
				}
				caveat = err
			}
			if _, err := index.UpdateIncremental(ctx, a.Registry, a.Index, sm.Provider); err != nil {
				fmt.Printf("⚠️  Warning: renamed, but index refresh failed: %s\n", err)
			}
			fmt.Printf("✅ Renamed %s to %s\n", sm.ID, title)
			if caveat != nil {
				fmt.Printf("   Caveat: %s\n", caveat)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title, used verbatim")
	cmd.Flags().BoolVar(&auto, "auto", false, "ask the configured title agent for one")
	cmd.Flags().StringVar(&from, "from", "", "source provider")
	cmd.Flags().BoolVar(&allowCurrent, "allow-current", false, "permit renaming the running session (SessionEnd hooks)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve and suggest without writing")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before rename")
	cmd.Flags().BoolVar(&skipConforming, "skip-conforming", false, "with --auto, leave a title that already follows the policy")
	return cmd
}

// titleConfig reads the title agent the user chose in setup. Both the policy
// check and the suggestion read it from here so a session cannot be judged
// against one language and named in another.
func titleConfig() (titler.Config, error) {
	settings, err := config.LoadSettings()
	if err != nil || settings.TitleModel == nil {
		return titler.Config{}, fmt.Errorf("no title agent configured: run another setup, or pass --title")
	}
	return titler.Config{
		Provider: settings.TitleModel.Provider,
		Model:    settings.TitleModel.Model,
		Language: titler.NormalizeLanguage(titler.Language(settings.TitleModel.Language)),
	}, nil
}

// suggestTitle runs the configured title agent for one session. The
// guards Suggest already owns — no creation time, an agent that cannot write
// titles, a CLI that is not installed — are left to it so this path cannot
// refuse for a reason the engine would not.
func (a *App) suggestTitle(ctx context.Context, p provider.Provider, sm model.Summary) (string, error) {
	cfg, err := titleConfig()
	if err != nil {
		return "", err
	}
	ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
	var conv *model.Conversation
	if preview, ok := p.(provider.PreviewLoader); ok {
		conv, err = preview.LoadPreview(ctx, ref, 12)
	} else {
		conv, err = p.Load(ctx, ref)
	}
	if err != nil {
		return "", err
	}
	var msgs []model.Message
	if conv != nil {
		msgs = conv.Messages
	}
	suggestion, err := titler.SuggestRetrying(ctx, cfg, titler.Request{
		Title: sm.Title, ProjectPath: sm.ProjectPath, CreatedAt: sm.CreatedAt, Messages: msgs,
	})
	if err != nil {
		return "", err
	}
	// Suggest returns "" without an error when the model declined or drifted
	// off the contract. The TUI can show nothing and wait; a hook cannot, so
	// the ambiguity has to become an exit code here.
	if strings.TrimSpace(suggestion) == "" {
		return "", fmt.Errorf("%s returned no title matching the policy", cfg.Provider)
	}
	return suggestion, nil
}
