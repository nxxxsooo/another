package cli

import (
	"context"
	"encoding/json"
	"github.com/nxxxsooo/another/internal/herdr"
	"github.com/spf13/cobra"
	"io"
	"os"
)

func (a *App) watchCmd() *cobra.Command {
	root := &cobra.Command{Use: "watch", Short: "Machine-facing live OpenCode watch groups"}
	status := &cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := (herdr.Client{}).Status(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(s)
	}}
	status.Flags().Bool("json", false, "JSON output (always enabled)")
	root.AddCommand(status)
	for _, action := range []string{"add", "rm"} {
		root.AddCommand(&cobra.Command{Use: action + " <supervisor> <member>", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			client := herdr.Client{}
			var err error
			if action == "add" {
				err = client.Watch(cmd.Context(), args[0], args[1])
			} else {
				err = client.Unwatch(cmd.Context(), args[0], args[1])
			}
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]bool{"ok": true})
		}})
	}
	root.AddCommand(&cobra.Command{Use: "follow <supervisor>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		// The TUI owns an open stdin pipe as a parent lease. EOF cancels hc
		// even after a TUI crash, without Unix-only process groups or signals.
		if os.Getenv("ANOTHER_WATCH_PARENT_PIPE") == "1" {
			go func() { _, _ = io.Copy(io.Discard, cmd.InOrStdin()); cancel() }()
		}
		return (herdr.Client{}).FollowCommand(ctx, args[0], func(event herdr.TurnEnd) error { return encoder.Encode(event) })
	}})
	return root
}
