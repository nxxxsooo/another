package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/util"
)

func (a *App) pathsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "paths",
		Short: "Follow projects that moved to a new directory",
		Long: "Agents record the directory a session ran in. Renaming or moving a " +
			"project strands its earlier sessions at a directory that no longer " +
			"exists. `another paths` reports those directories with the evidence " +
			"for where the project lives now; `another paths link` records your " +
			"decision.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.reportMissingDirectories(cmd)
		},
	}
	cmd.AddCommand(a.pathsLinkCmd())
	cmd.AddCommand(a.pathsUnlinkCmd())
	return cmd
}

func (a *App) reportMissingDirectories(cmd *cobra.Command) error {
	settings, _ := config.LoadSettings()
	if len(settings.PathAliases) > 0 {
		fmt.Println("Active aliases:")
		for _, alias := range settings.PathAliases {
			fmt.Printf("  %s -> %s\n", util.TildePath(alias.From), util.TildePath(alias.To))
		}
		fmt.Println()
	}
	missing, err := a.Index.MissingDirectories()
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		fmt.Println("Every indexed directory still exists.")
		return nil
	}
	fmt.Printf("Directories that no longer exist: %d\n\n", len(missing))
	for _, dir := range missing {
		fmt.Printf("%s\n", util.TildePath(dir.Path))
		fmt.Printf("  %d session(s) from %s\n", dir.Sessions, strings.Join(dir.Providers, ", "))
		if len(dir.Candidates) == 0 {
			fmt.Println("  No candidate found. Use `another paths link` if you know where it moved.")
			fmt.Println()
			continue
		}
		for _, candidate := range dir.Candidates {
			fmt.Printf("  Candidate: %s (%s)\n", util.TildePath(candidate.Path), candidate.Why)
			fmt.Printf("    another paths link %s %s\n",
				util.QuoteArg(candidate.Alias.From), util.QuoteArg(candidate.Alias.To))
		}
		fmt.Println()
	}
	return nil
}

func (a *App) pathsLinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "link <old-directory> <new-directory>",
		Short: "Record where a moved project lives now",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			from := strings.TrimRight(util.NormalizeProjectPath(args[0]), "/")
			to := strings.TrimRight(util.NormalizeProjectPath(args[1]), "/")
			if from == "" || to == "" {
				return fmt.Errorf("both directories are required")
			}
			if from == to {
				return fmt.Errorf("%s is already where the sessions point", util.TildePath(to))
			}
			if info, err := os.Stat(to); err != nil || !info.IsDir() {
				return fmt.Errorf("new directory %s does not exist", util.TildePath(to))
			}
			settings, err := loadSettingsForPaths()
			if err != nil {
				return err
			}
			replaced := false
			for i := range settings.PathAliases {
				if settings.PathAliases[i].From == from {
					settings.PathAliases[i].To = to
					replaced = true
				}
			}
			if !replaced {
				settings.PathAliases = append(settings.PathAliases, config.PathAlias{From: from, To: to})
			}
			if err := config.SaveSettings(settings); err != nil {
				return err
			}
			n, err := a.reprojectWithAliases(settings.PathAliases)
			if err != nil {
				return err
			}
			fmt.Printf("Linked %s -> %s\n", util.TildePath(from), util.TildePath(to))
			fmt.Printf("%d session(s) now belong to %s\n", n, util.TildePath(to))
			return nil
		},
	}
}

func (a *App) pathsUnlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <old-directory>",
		Short: "Drop an alias and restore the directories the agents recorded",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			from := strings.TrimRight(util.NormalizeProjectPath(args[0]), "/")
			settings, err := loadSettingsForPaths()
			if err != nil {
				return err
			}
			kept := settings.PathAliases[:0]
			removed := false
			for _, alias := range settings.PathAliases {
				if alias.From == from {
					removed = true
					continue
				}
				kept = append(kept, alias)
			}
			if !removed {
				return fmt.Errorf("no alias for %s", util.TildePath(from))
			}
			settings.PathAliases = kept
			if err := config.SaveSettings(settings); err != nil {
				return err
			}
			if _, err := a.reprojectWithAliases(settings.PathAliases); err != nil {
				return err
			}
			fmt.Printf("Unlinked %s\n", util.TildePath(from))
			return nil
		},
	}
}

// reprojectWithAliases re-applies the aliases to the sessions already indexed
// and reports how many now sit under a linked directory. The agents' own
// records are untouched, so this never reads a provider file.
func (a *App) reprojectWithAliases(aliases []config.PathAlias) (int, error) {
	a.Index.SetPathAliases(aliases)
	if err := a.Index.ReprojectSessions(); err != nil {
		return 0, err
	}
	total := 0
	for _, alias := range a.Index.PathAliases() {
		n, err := a.Index.Count(index.ListOpts{ProjectRoots: []string{alias.To}, IncludeSubagents: true})
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// loadSettingsForPaths treats a missing config as an empty one: linking a
// directory is a complete instruction on its own and must not require setup
// first.
func loadSettingsForPaths() (config.Settings, error) {
	settings, err := config.LoadSettings()
	if err == nil {
		return settings, nil
	}
	if os.IsNotExist(err) {
		return config.Settings{Version: config.SettingsVersion}, nil
	}
	return config.Settings{}, err
}
