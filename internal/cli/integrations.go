package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/integrations"
	"github.com/nxxxsooo/another/internal/util"
	"github.com/spf13/cobra"
)

// integrationsCmd exposes the adapters another installs into other agents
// outside setup, for scripts, upgrades, and anyone who wants to see where the
// files went before allowing them.
//
// Every subcommand takes agents by name — the agent an adapter writes into is
// what a person is actually choosing. Naming none means every adapter another
// ships, which is what an upgrade or a scripted reinstall wants.
func (a *App) integrationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integrations",
		Short: "Adapters another installs into other agents",
		Args:  cobra.NoArgs,
	}
	var configDir string
	var forceInstall, forceRemove bool
	cmd.PersistentFlags().StringVar(&configDir, "config-dir", "",
		"config directory of the named agent (requires naming one adapter)")

	// targets resolves the adapter names on the command line. --config-dir
	// overrides where one agent's configuration is, so it cannot be given to
	// a run that covers several: the directory would be wrong for all but one.
	targets := func(args []string) ([]integrations.Adapter, error) {
		if len(args) == 0 {
			if configDir != "" {
				return nil, fmt.Errorf("--config-dir needs one agent: %s", strings.Join(integrations.Providers(), ", "))
			}
			return integrations.Adapters(), nil
		}
		chosen := make([]integrations.Adapter, 0, len(args))
		for _, name := range args {
			adapter, ok := integrations.ForProvider(name)
			if !ok {
				return nil, fmt.Errorf("no adapter for %q: %s", name, strings.Join(integrations.Providers(), ", "))
			}
			chosen = append(chosen, adapter)
		}
		if configDir != "" && len(chosen) > 1 {
			return nil, fmt.Errorf("--config-dir needs one agent, not %d", len(chosen))
		}
		return chosen, nil
	}

	resolve := func(ctx context.Context, adapter integrations.Adapter) integrations.Status {
		dir := configDir
		if dir == "" {
			dir = adapter.ConfigDir(ctx)
		}
		return adapter.Status(config.ExpandPath(dir))
	}

	status := &cobra.Command{
		Use:       "status [agent...]",
		Short:     "Report the installed adapters",
		ValidArgs: integrations.Providers(),
		Args:      cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			chosen, err := targets(args)
			if err != nil {
				return err
			}
			for _, adapter := range chosen {
				printIntegration(adapter, resolve(cmd.Context(), adapter))
			}
			return nil
		},
	}
	install := &cobra.Command{
		Use:       "install [agent...]",
		Short:     "Install or refresh the title-policy adapters",
		ValidArgs: integrations.Providers(),
		Args:      cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			chosen, err := targets(args)
			if err != nil {
				return err
			}
			settings, err := config.LoadSettings()
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("load config: %w", err)
			}
			for _, adapter := range chosen {
				result, err := adapter.Install(resolve(cmd.Context(), adapter), version, settings.TitlePolicy.Language, forceInstall)
				if err != nil {
					return err
				}
				// Installing is consent, and consent has to survive the
				// command that gave it: setup keeps an adapter current on
				// later runs only because the answer is recorded.
				if consent := adapter.Consent(&settings.Integrations); !*consent {
					*consent = true
					if err := config.SaveSettings(settings); err != nil {
						return fmt.Errorf("save config: %w", err)
					}
				}
				fmt.Printf("Installed %s\n", util.TildePath(result.Dir))
				printIntegration(adapter, result)
			}
			return nil
		},
	}
	install.Flags().BoolVar(&forceInstall, "force", false, "overwrite files another did not write")
	remove := &cobra.Command{
		Use:       "remove [agent...]",
		Short:     "Remove the title-policy adapters",
		ValidArgs: integrations.Providers(),
		Args:      cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			chosen, err := targets(args)
			if err != nil {
				return err
			}
			for _, adapter := range chosen {
				found := resolve(cmd.Context(), adapter)
				if err := adapter.Remove(found, forceRemove); err != nil {
					return err
				}
				settings, err := config.LoadSettings()
				if err == nil {
					if consent := adapter.Consent(&settings.Integrations); *consent {
						*consent = false
						if err := config.SaveSettings(settings); err != nil {
							return fmt.Errorf("save config: %w", err)
						}
					}
				}
				if !found.State.Installed() {
					fmt.Printf("Nothing installed at %s\n", util.TildePath(found.Dir))
					continue
				}
				fmt.Printf("Removed %s\n", util.TildePath(found.Dir))
			}
			return nil
		},
	}
	remove.Flags().BoolVar(&forceRemove, "force", false, "remove files another did not write")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		for _, adapter := range integrations.Adapters() {
			printIntegration(adapter, resolve(cmd.Context(), adapter))
		}
		return nil
	}
	cmd.AddCommand(status, install, remove)
	return cmd
}

// applyIntegrations carries out what setup was told about the adapters. It
// runs after the settings are saved, so a failure here loses nothing a person
// chose: the answer is recorded either way and the next run acts on it.
//
// Nothing is removed here. Turning a row off means another stops maintaining
// that adapter, and deleting a file from someone else's configuration deserves
// the explicit command that says so.
func applyIntegrations(settings config.Settings, resolvers map[string]func() integrations.Status) {
	for _, adapter := range integrations.Adapters() {
		resolve, ok := resolvers[adapter.ID]
		if !ok {
			continue
		}
		found := resolve()
		label := adapterLabel(adapter)
		if !*adapter.Consent(&settings.Integrations) {
			if found.State.Installed() {
				fmt.Printf("%s: %s\n", label, util.TildePath(found.Dir))
				fmt.Printf("    still installed; remove it with 'another integrations remove %s'\n", adapter.Provider)
			}
			continue
		}
		result, err := adapter.Install(found, version, settings.TitlePolicy.Language, false)
		if err != nil {
			fmt.Printf("%s: %v\n", label, err)
			continue
		}
		fmt.Printf("%s: %s\n", label, util.TildePath(result.Dir))
		if result.RedundantEntry != "" {
			fmt.Printf("    %s still lists this plugin; OpenCode 2 finds it without that entry\n",
				util.TildePath(result.RedundantEntry))
		}
	}
}

// adapterLabel names an adapter the way a report should read, rather than by
// its manifest id.
func adapterLabel(adapter integrations.Adapter) string {
	if adapter.ID == integrations.Pi {
		return "Pi title extension"
	}
	return "OpenCode 2 title plugin"
}

func printIntegration(adapter integrations.Adapter, status integrations.Status) {
	fmt.Printf("%-24s %s\n", adapter.ID, integrationStateText(status))
	fmt.Printf("    files: %s\n", util.TildePath(status.Dir))
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
