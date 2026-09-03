package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/spf13/cobra"
)

func (a *App) importCmd() *cobra.Command {
	var to, project, contextMode string
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "import <session.another.json>",
		Short: "Import portable JSON session into a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := migrate.ParseContextMode(contextMode)
			if err != nil {
				return err
			}
			if to == "" {
				return fmt.Errorf("--to is required")
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			var conv model.Conversation
			if err := json.NewDecoder(f).Decode(&conv); err != nil {
				return fmt.Errorf("decode: %w", err)
			}
			if !yes && !dryRun {
				if !confirmAction(fmt.Sprintf("Import %d messages → %s? [y/N] ", len(conv.Messages), to)) {
					fmt.Println("cancelled")
					return nil
				}
			}
			ctx := cmd.Context()
			res, err := a.Migrate.Import(ctx, &conv, migrate.Options{
				ToProvider: to, ProjectPath: project, DryRun: dryRun, ContextMode: mode,
			})
			if err != nil {
				return err
			}
			if dryRun {
				printProjectionSummary(res)
				if res.AlreadyExists {
					fmt.Printf("Dry run OK: already exists at %s\n", res.Write.StoragePath)
				} else {
					fmt.Printf("Dry run OK: would write to %s\n", res.Write.StoragePath)
				}
				for _, warning := range res.Warnings {
					fmt.Printf("⚠️  Warning: %s\n", warning)
				}
				return nil
			}
			if res.AlreadyExists {
				fmt.Printf("ℹ️  Already imported to %s\n", res.TargetName)
			} else {
				fmt.Printf("✅ Imported to %s\n", res.TargetName)
			}
			fmt.Printf("   Session: %s\n   Resume:  %s\n", res.Write.SessionID, res.Resume)
			printProjectionSummary(res)
			for _, warning := range res.Warnings {
				fmt.Printf("⚠️  Warning: %s\n", warning)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "target provider (required)")
	cmd.Flags().StringVar(&project, "project", "", "target project path")
	cmd.Flags().StringVar(&contextMode, "context", "auto", "context mode: auto, full, or recent")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}
