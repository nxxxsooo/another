package cli

import (
	"fmt"

	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
	"github.com/spf13/cobra"
)

func (a *App) resumeCmd() *cobra.Command {
	var from, to string
	var refresh bool
	cmd := &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Print resume command for a session on the target provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if to == "" {
				return fmt.Errorf("--to is required")
			}
			if err := a.ensureIndex(ctx, from, refresh); err != nil {
				return err
			}
			sm, src, err := migrate.ResolveSession(ctx, a.Registry, a.Index, args[0], from)
			if err != nil {
				return err
			}
			conv, err := src.Load(ctx, provider.SessionRef{
				ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
			})
			if err != nil {
				return err
			}
			dst, err := a.Registry.Get(registry.NormalizeID(to))
			if err != nil {
				return err
			}
			dup, ok := migrate.FindDuplicate(a.Index, dst, conv)
			if !ok {
				return fmt.Errorf("not migrated to %s yet — run: another migrate %s --to %s -y", to, args[0], to)
			}
			fmt.Println(dst.ResumeCommand(*dup))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "source provider")
	cmd.Flags().StringVar(&to, "to", "", "target provider (required)")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index before lookup")
	return cmd
}
