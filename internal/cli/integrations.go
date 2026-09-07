package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/integrations"
	"github.com/nxxxsooo/another/internal/util"
	"github.com/spf13/cobra"
)

// integrationsCmd exposes the adapter another installs into OpenCode 2 outside
// setup, for scripts, upgrades, and anyone who wants to see where the plugin
// went before allowing it.
func (a *App) integrationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integrations",
		Short: "Adapters another installs into other agents",
		Args:  cobra.NoArgs,
	}
	var configDir string
	var forceInstall, forceRemove bool
	cmd.PersistentFlags().StringVar(&configDir, "config-dir", "", "OpenCode 2 config directory")

	resolve := func(ctx context.Context) integrations.Status {
		dir := configDir
		if dir == "" {
			dir = integrations.OpenCode2ConfigDir(ctx)
		}
		return integrations.OpenCode2Status(config.ExpandPath(dir))
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Report the installed adapter",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			printIntegration(resolve(cmd.Context()))
			return nil
		},
	}
	install := &cobra.Command{
		Use:   "install",
		Short: "Install or refresh the OpenCode 2 title-policy plugin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("load config: %w", err)
			}
			result, err := integrations.InstallOpenCode2(resolve(cmd.Context()), version, settings.TitlePolicy.Language, forceInstall)
			if err != nil {
				return err
			}
			// Installing is consent, and consent has to survive the command
			// that gave it: setup keeps the plugin current on later runs only
			// because the answer is recorded.
			if !settings.Integrations.OpenCode2TitlePolicy {
				settings.Integrations.OpenCode2TitlePolicy = true
				if err := config.SaveSettings(settings); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
			}
			fmt.Printf("Installed %s\n", util.TildePath(result.Dir))
			printIntegration(result)
			return nil
		},
	}
	install.Flags().BoolVar(&forceInstall, "force", false, "overwrite files another did not write")
	remove := &cobra.Command{
		Use:   "remove",
		Short: "Remove the OpenCode 2 title-policy plugin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			found := resolve(cmd.Context())
			if err := integrations.RemoveOpenCode2(found, forceRemove); err != nil {
				return err
			}
			settings, err := config.LoadSettings()
			if err == nil && settings.Integrations.OpenCode2TitlePolicy {
				settings.Integrations.OpenCode2TitlePolicy = false
				if err := config.SaveSettings(settings); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
			}
			if !found.State.Installed() {
				fmt.Printf("Nothing installed at %s\n", util.TildePath(found.Dir))
				return nil
			}
			fmt.Printf("Removed %s\n", util.TildePath(found.Dir))
			return nil
		},
	}
	remove.Flags().BoolVar(&forceRemove, "force", false, "remove files another did not write")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		printIntegration(resolve(cmd.Context()))
		return nil
	}
	cmd.AddCommand(status, install, remove)
	return cmd
}

// applyIntegrations carries out what setup was told about the adapter. It runs
// after the settings are saved, so a failure here loses nothing a person
// chose: the answer is recorded either way and the next run acts on it.
//
// Nothing is removed here. Turning the row off means another stops maintaining
// the plugin, and deleting a file from someone else's configuration deserves
// the explicit command that says so.
func applyIntegrations(settings config.Settings, found integrations.Status) {
	if !settings.Integrations.OpenCode2TitlePolicy {
		if found.State.Installed() {
			fmt.Printf("OpenCode 2 title plugin: %s\n", util.TildePath(found.Dir))
			fmt.Println("    still installed; remove it with 'another integrations remove'")
		}
		return
	}
	result, err := integrations.InstallOpenCode2(found, version, settings.TitlePolicy.Language, false)
	if err != nil {
		fmt.Printf("OpenCode 2 title plugin: %v\n", err)
		return
	}
	fmt.Printf("OpenCode 2 title plugin: %s\n", util.TildePath(result.Dir))
	if result.RedundantEntry != "" {
		fmt.Printf("    %s still lists this plugin; OpenCode 2 finds it without that entry\n",
			util.TildePath(result.RedundantEntry))
	}
}

func printIntegration(status integrations.Status) {
	fmt.Printf("%-24s %s\n", integrations.OpenCode2, integrationStateText(status))
	fmt.Printf("    plugin: %s\n", util.TildePath(status.Dir))
	if status.Language != "" {
		fmt.Printf("    title language: %s\n", status.Language)
	}
	if status.RedundantEntry != "" {
		// The entry is harmless while the directory stays where it is, so
		// this is a cleanup note rather than a failure.
		fmt.Printf("    note: %s still lists this plugin; OpenCode 2 finds it without that entry\n",
			util.TildePath(status.RedundantEntry))
	}
}

func integrationStateText(status integrations.Status) string {
	switch status.State {
	case integrations.StateCurrent:
		return "installed (" + versionText(status.Version) + ")"
	case integrations.StateOutdated:
		return "outdated (" + versionText(status.Version) + ") — run 'another integrations install'"
	case integrations.StateModified:
		return "edited locally — 'another integrations install --force' restores it"
	case integrations.StateAdoptable:
		return "installed by hand — run 'another integrations install' to let another keep it current"
	case integrations.StateForeign:
		return "another plugin occupies this directory"
	default:
		return "not installed"
	}
}

func versionText(v string) string {
	if v == "" {
		return "unknown version"
	}
	return "another " + v
}
