package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// installSource is where the running binary came from, which decides how it is
// replaced. another does not overwrite itself: the binary is a running Mach-O
// whose signature a rewrite invalidates, and on Homebrew it is a file that
// belongs to a package manager tracking its own version. Each source is
// updated by the tool that installed it.
type installSource int

const (
	sourceUnknown installSource = iota
	sourceHomebrew
	sourceScript
	sourceGoInstall
)

const releaseAPI = "https://api.github.com/repos/nxxxsooo/another/releases/latest"

func (a *App) updateCmd() *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update another itself to the latest release",
		Long: `Update the another binary through whatever installed it.

Homebrew installs are upgraded with brew, script installs are replaced by
re-running the install script, and a go install build is left to go. another
never overwrites its own running binary: that invalidates the signature macOS
checks on the next run, and it hides the new version from the package manager
that believes it owns the file.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSelfUpdate(cmd, checkOnly)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "report the available version without installing")
	return cmd
}

func runSelfUpdate(cmd *cobra.Command, checkOnly bool) error {
	out := cmd.OutOrStdout()
	current := strings.TrimPrefix(version, "v")
	latest, err := latestRelease(cmd.Context())
	if err != nil {
		return fmt.Errorf("check for the latest release: %w", err)
	}
	fmt.Fprintf(out, "installed: %s\nlatest:    %s\n", versionOrDev(current), latest)

	if current != "" && current == strings.TrimPrefix(latest, "v") {
		fmt.Fprintln(out, "\nAlready up to date.")
		return nil
	}
	if checkOnly {
		return nil
	}

	exe, source := detectInstallSource()
	switch source {
	case sourceHomebrew:
		fmt.Fprintln(out, "\nInstalled with Homebrew; upgrading the cask.")
		return runUpdateCommand(cmd, "brew", "upgrade", "--cask", "another")
	case sourceScript:
		fmt.Fprintf(out, "\nInstalled at %s; re-running the install script.\n", exe)
		script := "curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | INSTALL_DIR=" +
			shellQuote(filepath.Dir(exe)) + " bash"
		return runUpdateCommand(cmd, "sh", "-c", script)
	case sourceGoInstall:
		// go install builds carry whatever the source tree said, so the module
		// path is the only honest way to move them forward.
		fmt.Fprintln(out, "\nInstalled with go install; run:")
		fmt.Fprintln(out, "  go install github.com/nxxxsooo/another/cmd/another@latest")
		return nil
	default:
		fmt.Fprintf(out, "\nCould not tell how %s was installed. Use whichever applies:\n", exe)
		fmt.Fprintln(out, "  brew upgrade --cask another")
		fmt.Fprintln(out, "  curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash")
		fmt.Fprintln(out, "  go install github.com/nxxxsooo/another/cmd/another@latest")
		return nil
	}
}

// versionOrDev keeps an unstamped build honest rather than printing an empty
// column beside a real release number.
func versionOrDev(current string) string {
	if current == "" || current == "dev" {
		return "dev (unreleased build)"
	}
	return current
}

func latestRelease(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github answered %s", resp.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("no tag in the latest release")
	}
	return payload.TagName, nil
}

// detectInstallSource reads the path of the running binary. The path is what
// the installers disagree about, and it is available without asking any of
// them whether they are present.
func detectInstallSource() (string, installSource) {
	exe, err := os.Executable()
	if err != nil {
		return "", sourceUnknown
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, classifyExecutable(exe)
}

// classifyExecutable is the path rule on its own, so the decision can be read
// and tested without a binary actually living there.
func classifyExecutable(exe string) installSource {
	switch {
	case strings.Contains(exe, "/Caskroom/"), strings.Contains(exe, "/Cellar/"),
		strings.HasPrefix(exe, "/opt/homebrew/"), strings.HasPrefix(exe, "/home/linuxbrew/"):
		return sourceHomebrew
	case exe == filepath.Join(goBinDir(), "another"):
		return sourceGoInstall
	case filepath.Base(exe) == "another":
		// The install script's default is ~/.local/bin, but it honors
		// INSTALL_DIR, so the directory is not what identifies it — anything
		// still named `another` outside a package manager is replaceable by
		// re-running the script into its own directory.
		return sourceScript
	}
	return sourceUnknown
}

func goBinDir() string {
	if dir := os.Getenv("GOBIN"); dir != "" {
		return dir
	}
	if dir := os.Getenv("GOPATH"); dir != "" {
		return filepath.Join(dir, "bin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "bin")
}

// runUpdateCommand hands the terminal to the installer so its own progress and
// prompts reach the user unchanged.
func runUpdateCommand(cmd *cobra.Command, name string, args ...string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("another update is not supported on this platform yet")
	}
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is not on PATH: %w", name, err)
	}
	c := exec.CommandContext(cmd.Context(), name, args...)
	c.Stdin = cmd.InOrStdin()
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

// shellQuote wraps a path for the one place another builds a shell command:
// the install script runs through sh, and a home directory can contain spaces.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
