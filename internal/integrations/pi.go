package integrations

import (
	"os"
	"path/filepath"

	extension "github.com/nxxxsooo/another/integrations/pi-title-policy"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/providers/pi"
)

// Pi is the title-policy extension adapter. Pi names a session through an
// extension rather than a built-in step, so another ships one that calls
// `another rename --auto` at the end of a turn: the policy, the title agent,
// and the language all stay in the binary, and the extension only says when.
const Pi = "pi-title-policy"

// PiConfigDirEnv overrides directory resolution entirely, for tests and for a
// Pi whose agent directory another cannot find.
const PiConfigDirEnv = "ANOTHER_PI_CONFIG_DIR"

// PiConfigDir resolves Pi's agent directory.
//
// Unlike OpenCode 2 there is no CLI to ask: Pi keeps its extensions beside its
// sessions under one agent directory. The provider owns resolving it, and this
// defers to it so the extension can never be installed into a directory the
// provider does not read.
func PiConfigDir() string {
	if dir := os.Getenv(PiConfigDirEnv); dir != "" {
		return config.ExpandPath(dir)
	}
	return pi.AgentDir()
}

// PiDir is where the extension belongs inside a resolved agent directory. Pi
// loads every extension directory under extensions/, so installing the files
// is the whole installation: another never edits Pi's own configuration.
func PiDir(configDir string) string {
	return filepath.Join(configDir, "extensions", extension.DirName)
}

// piBundle is the Pi extension as the shared install and compare paths see it.
var piBundle = bundle{
	Integration: Pi,
	Names:       extension.Names,
	Files:       extension.Files,
	Hashes:      extension.Hashes,
	Hash:        extension.Hash,
}

// PiStatus reads the installed extension and compares it with the one this
// binary ships.
func PiStatus(configDir string) Status {
	status := Status{ConfigDir: configDir, Dir: PiDir(configDir), State: StateMissing}
	installed, err := readInstalled(status.Dir, piBundle)
	if err != nil || len(installed) == 0 {
		return status
	}
	current := sameHashes(installed, piBundle.Hashes())
	saved, ok := readManifest(status.Dir, piBundle)
	if !ok {
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

// InstallPi writes the shipped extension into Pi's agent directory and records
// what it wrote, under the same rules as every other adapter.
func InstallPi(status Status, version, language string, force bool) (Status, error) {
	if err := installBundle(status, piBundle, version, language, force); err != nil {
		return status, err
	}
	return PiStatus(status.ConfigDir), nil
}

// RemovePi takes back only what another installed.
func RemovePi(status Status, force bool) error {
	return removeBundle(status, piBundle, force)
}
