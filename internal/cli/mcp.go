package cli

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nxxxsooo/another/internal/mcpserver"
	"github.com/spf13/cobra"
)

// mcpCmd serves the current project's sessions over stdio. Agents launch it in
// the directory they run in, which fixes the scope for the whole connection;
// stdout belongs to the protocol, so nothing else may print there.
func (a *App) mcpCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve this project's sessions as MCP resources over stdio, for @ mentions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.ensureIndex(ctx, "", false); err != nil {
				return err
			}
			scope, err := currentScope(ctx)
			if err != nil {
				return err
			}
			server, err := (&mcpserver.Server{
				Registry: a.Registry, Index: a.Index, Scope: scope, Limit: limit, Version: version,
			}).Build()
			if err != nil {
				return err
			}
			return server.Run(ctx, &mcp.StdioTransport{})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", mcpserver.DefaultLimit, "most recent sessions listed")
	return cmd
}
