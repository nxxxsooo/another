package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	plugin "github.com/nxxxsooo/another/integrations/opencode2-sessions"
)

const OpenCodeSessions = "opencode2-sessions"

func OpenCodeSessionsDir(configDir string) string {
	return filepath.Join(configDir, "plugins", plugin.DirName)
}

// Record the installing executable, not a shell alias or an unrelated release
// found on PATH. Test executables use the same deterministic name as MCPCommand.
func sessionsFiles() map[string][]byte {
	files := plugin.Files()
	executable := MCPCommand()[0]
	if path, err := os.Executable(); err == nil && strings.HasPrefix(filepath.Base(path), "another") {
		executable = path
	}
	encoded, _ := json.Marshal(executable)
	files["runtime.ts"] = []byte("// Installed by another.\nexport const executable = " + string(encoded) + "\n")
	return files
}

var sessionsBundle = bundle{
	Integration: OpenCodeSessions, Plugin: plugin.PluginID,
	Names: func() []string { return append(plugin.Names(), "runtime.ts") },
	Files: sessionsFiles, Hash: plugin.Hash,
	Hashes: func() map[string]string {
		hashes := map[string]string{}
		for name, content := range sessionsFiles() {
			hashes[name] = plugin.Hash(content)
		}
		return hashes
	},
}

func OpenCodeSessionsStatus(configDir string) Status {
	status := Status{ConfigDir: configDir, Dir: OpenCodeSessionsDir(configDir), State: StateMissing}
	installed, err := readInstalled(status.Dir, sessionsBundle)
	if err != nil || len(installed) == 0 {
		return status
	}
	current := sameHashes(installed, sessionsBundle.Hashes())
	saved, ok := readManifest(status.Dir, sessionsBundle)
	switch {
	case !ok && current:
		status.State = StateAdoptable
	case !ok:
		status.State = StateForeign
	case !sameHashes(installed, saved.Files):
		status.State = StateModified
	case current:
		status.State = StateCurrent
	default:
		status.State = StateOutdated
	}
	status.Version = saved.Version
	return status
}

func InstallOpenCodeSessions(status Status, version, language string, force bool) (Status, error) {
	if err := installBundle(status, sessionsBundle, version, language, force); err != nil {
		return status, err
	}
	return OpenCodeSessionsStatus(status.ConfigDir), nil
}

func RemoveOpenCodeSessions(status Status, force bool) error {
	return removeBundle(status, sessionsBundle, force)
}
