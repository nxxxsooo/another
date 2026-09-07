package integrations

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	plugin "github.com/nxxxsooo/another/integrations/opencode2-title-policy"
	"github.com/nxxxsooo/another/internal/config"
)

// OpenCode2 is the title-policy plugin adapter: OpenCode 2 names a session
// itself on the first exchange, and this plugin makes that native request
// follow another's shared policy instead of adding a second one.
const OpenCode2 = "opencode2-title-policy"

// ConfigDirEnv overrides directory resolution entirely. Tests use it, and so
// does anyone running an OpenCode 2 whose configuration another cannot find.
const ConfigDirEnv = "ANOTHER_OPENCODE2_CONFIG_DIR"

// OpenCode2ConfigDir resolves the OpenCode 2 global configuration directory.
//
// The directory cannot be assumed: OpenCode 2 reads OPENCODE_CONFIG_DIR, and a
// machine that runs V1 and V2 side by side keeps them apart with exactly that
// variable, usually from a wrapper script that another is not running under.
// Guessing ~/.config/opencode there would install a V2 plugin into the V1
// configuration, where it cannot load. So another asks the CLI first, which
// answers for the OpenCode 2 the user actually runs, and only then falls back
// to the variables and defaults.
func OpenCode2ConfigDir(ctx context.Context) string {
	if dir := os.Getenv(ConfigDirEnv); dir != "" {
		return config.ExpandPath(dir)
	}
	if dir := probeOpenCode2ConfigDir(ctx); dir != "" {
		return dir
	}
	if dir := os.Getenv("OPENCODE_CONFIG_DIR"); dir != "" {
		return config.ExpandPath(dir)
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	return filepath.Join(config.HomeDir(), ".config", "opencode")
}

// probeOpenCode2ConfigDir asks a running OpenCode 2 where its configuration
// document is. The command is cheap while the service is up and is allowed to
// fail: every caller has a fallback, and setup must not hang on an agent that
// happens to be stopped.
func probeOpenCode2ConfigDir(ctx context.Context) string {
	command := config.EnvOrDefault("OPENCODE2_COMMAND", "opencode2")
	if _, err := exec.LookPath(command); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, "api", "get", "/api/config")
	// The reply describes the configuration for the working directory, so a
	// project of its own would add its documents to the answer. Home is the
	// one directory that reliably has none.
	cmd.Dir = config.HomeDir()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var entries []struct {
		Type string `json:"type"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return ""
	}
	var fallback string
	for _, entry := range entries {
		if entry.Type != "document" || entry.Path == "" {
			continue
		}
		dir := filepath.Dir(entry.Path)
		// A configuration directory is the one under the user's config home;
		// anything else in the answer belongs to a project.
		if strings.HasPrefix(dir, configHome()+string(filepath.Separator)) {
			return dir
		}
		if fallback == "" {
			fallback = dir
		}
	}
	return fallback
}

func configHome() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}
	return filepath.Join(config.HomeDir(), ".config")
}

// OpenCode2Dir is where the plugin belongs inside a resolved configuration
// directory. OpenCode 2 discovers every plugin directory under plugins/ on its
// own, so installing the files is the whole installation: another never edits
// the user's opencode.json(c).
func OpenCode2Dir(configDir string) string {
	return filepath.Join(configDir, "plugins", plugin.DirName)
}

// OpenCode2Status reads the installed plugin and compares it with the one this
// binary ships.
func OpenCode2Status(configDir string) Status {
	status := Status{ConfigDir: configDir, Dir: OpenCode2Dir(configDir), State: StateMissing}
	status.RedundantEntry = redundantEntry(configDir)
	installed, err := readInstalled(status.Dir)
	if err != nil || len(installed) == 0 {
		return status
	}
	current := matchesShipped(installed)
	saved, ok := readManifest(status.Dir)
	if !ok {
		// Files without a manifest are the hand-copied installation this
		// feature replaces. Identical content can be adopted, because writing
		// the manifest is then the only change; different content belongs to
		// someone else and is not another's to interpret.
		if current {
			status.State = StateAdoptable
			return status
		}
		status.State = StateForeign
		return status
	}
	status.Language, status.Version = saved.Language, saved.Version
	switch {
	case !sameHashes(installed, saved.Files):
		status.State = StateModified
	case current:
		status.State = StateCurrent
	default:
		status.State = StateOutdated
	}
	return status
}

// readInstalled hashes the files another would write, as they exist on disk.
// A file another does not ship is ignored: the comparison is about the plugin,
// not about everything that shares its directory.
func readInstalled(dir string) (map[string]string, error) {
	hashes := make(map[string]string)
	for _, name := range plugin.Names() {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		hashes[name] = plugin.Hash(content)
	}
	return hashes, nil
}

func matchesShipped(installed map[string]string) bool {
	return sameHashes(installed, plugin.Hashes())
}

func sameHashes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, hash := range a {
		if b[name] != hash {
			return false
		}
	}
	return true
}

func readManifest(dir string) (manifest, bool) {
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return manifest{}, false
	}
	var saved manifest
	if err := json.Unmarshal(data, &saved); err != nil {
		return manifest{}, false
	}
	if saved.Integration != OpenCode2 || len(saved.Files) == 0 {
		return manifest{}, false
	}
	return saved, true
}

// redundantEntry reports an explicit plugins entry naming this plugin. The
// file is JSONC and belongs to the user, so it is read as text and never
// rewritten: OpenCode 2 loads the plugin once whether or not the entry is
// there, so the entry costs nothing until someone moves the directory.
func redundantEntry(configDir string) string {
	for _, name := range []string{"opencode.jsonc", "opencode.json"} {
		path := filepath.Join(configDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), plugin.DirName) {
			return path
		}
	}
	return ""
}
