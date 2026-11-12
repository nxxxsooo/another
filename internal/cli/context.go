package cli

import (
	"fmt"
	"os"

	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/recall"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/spf13/cobra"
)

// contextCmd prints the bounded Markdown a mention puts into a prompt. It is
// the one entry point the recall skill and the Pi extension call, so what an
// agent reads through either matches what the MCP server hands out.
func (a *App) contextCmd() *cobra.Command {
	var from string
	var all, refresh, cached bool
	cmd := &cobra.Command{
		Use:   "context <session-id>",
		Short: "Print a bounded Markdown view of a session for another agent to read",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.readIndex(ctx, from, refresh, cached); err != nil {
				return err
			}
			sm, p, err := migrate.ResolveSession(ctx, a.Registry, a.Index, args[0], from)
			if err != nil {
				return err
			}
			if err := requireInScope(ctx, sm, all); err != nil {
				return err
			}
			conv, err := p.Load(ctx, provider.SessionRef{ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath})
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(os.Stdout, recall.Render(conv, recall.Source{AgentName: registry.DisplayName(a.Registry, sm.Provider)}, recall.Mention))
			return err
		},
	}
	cmd.Flags().StringVar(&from, "provider", "", "source provider")
	cmd.Flags().BoolVar(&all, "all", false, "allow a session from any project, not only the current one")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refresh index first")
	cmd.Flags().BoolVar(&cached, "cached", false, "resolve from the cached index without scanning provider stores")
	return cmd
}
