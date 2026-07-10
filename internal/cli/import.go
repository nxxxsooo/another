package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
	"github.com/spf13/cobra"
)

func (a *App) importCmd() *cobra.Command {
	var to, project string
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "import <session.agenthop.json>",
		Short: "Import portable JSON session into a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			dst, err := a.Registry.Get(registry.NormalizeID(to))
			if err != nil {
				return err
			}
			if !dst.Installed() {
				return provider.ErrNotInstalled
			}
			if !yes && !dryRun {
				if !confirmAction(fmt.Sprintf("Import %d messages → %s? [y/N] ", len(conv.Messages), to)) {
					fmt.Println("cancelled")
					return nil
				}
			}
			ctx := cmd.Context()
			if dup, ok := migrate.FindDuplicate(a.Index, dst, &conv); ok {
				if dryRun {
					fmt.Printf("Dry run OK: already exists at %s\n", dup.StoragePath)
					return nil
				}
				if ens, ok := dst.(provider.ResumeEnsurer); ok {
					if err := ens.EnsureResumable(&conv, *dup); err != nil {
						return fmt.Errorf("ensure resumable: %w", err)
					}
				}
				_ = a.Index.RecordMigration(dst.ID(), model.OriginDigest(&conv), dup.SessionID, dup.StoragePath, conv.ID, conv.Provider)
				fmt.Printf("ℹ️  Already imported to %s\n   Session: %s\n   Resume:  %s\n",
					dst.DisplayName(), dup.SessionID, dst.ResumeCommand(*dup))
				return nil
			}
			write, err := dst.Write(ctx, &conv, provider.WriteOpts{
				ProjectPath: project,
				DryRun:      dryRun,
			})
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Printf("Dry run OK: would write to %s\n", write.StoragePath)
				return nil
			}
			if err := a.Index.RecordMigration(dst.ID(), model.OriginDigest(&conv), write.SessionID, write.StoragePath, conv.ID, conv.Provider); err != nil {
				return fmt.Errorf("record migration: %w", err)
			}
			if _, err := index.UpdateIncremental(ctx, a.Registry, a.Index, dst.ID()); err != nil {
				return fmt.Errorf("update index: %w", err)
			}
			fmt.Printf("✅ Imported to %s\n   Session: %s\n   Resume:  %s\n",
				dst.DisplayName(), write.SessionID, dst.ResumeCommand(*write))
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "target provider (required)")
	cmd.Flags().StringVar(&project, "project", "", "target project path")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}
