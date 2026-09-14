package cli

import (
	"fmt"
	"strings"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/shortcuts"
	"github.com/spf13/cobra"
)

func (a *App) ensureAliases(cmd *cobra.Command) {
	if !stdinIsTerminal() {
		return
	}
	dev := strings.HasSuffix(version, "+dev")
	if err := shortcuts.Ensure(cmd.Context(), "", dev, config.ConfigDir(), cmd.OutOrStdout()); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Shell shortcuts skipped: %v\n", err)
	}
}

func (a *App) aliasesCmd() *cobra.Command {
	var dev bool
	var shell string
	cmd := &cobra.Command{Use: "aliases", Short: "Manage optional shell shortcuts"}
	install := &cobra.Command{
		Use: "install", Short: "Install aliases unless their names are already in use", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return shortcuts.Install(cmd.Context(), shell, dev, cmd.OutOrStdout())
		},
	}
	install.Flags().BoolVar(&dev, "dev", false, "install a-dev and adev for another-dev instead of a")
	install.Flags().StringVar(&shell, "shell", "", "shell executable (defaults to SHELL or Windows PowerShell)")
	cmd.AddCommand(install)
	return cmd
}
