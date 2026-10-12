package integrations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/util"
)

// Agy is the title-policy hook adapter. agy already names its conversations
// itself, so this adapter does not add a second naming pipeline — it adds the
// shared policy one: at the end of an idle turn, the hook asks another to
// rename the conversation, and everything the policy needs (the title agent,
// the language, the validation) stays in the binary.
//
// The hook is a group in agy's shared hooks.json rather than a native plugin.
// The plugin surface would need another to replicate agy's private install
// and registration mechanics, while the shared file is the surface other
// tools already use successfully: groups coexist, each keeping its own name.
const Agy = "agy-title-policy"

// AgyGroup is the group the hook is installed under. Its name is the
// contract: status and removal find the installation by it alone.
const AgyGroup = "another-title-policy"

// AgyConfigDirEnv overrides directory resolution entirely, for tests and for
// an agy whose layout another cannot find.
const AgyConfigDirEnv = "ANOTHER_AGY_CONFIG_DIR"

// AgyConfigDir resolves the directory holding the hooks.json agy reads.
//
// agy reads the hooks.json that is a sibling of its home —
// ~/.gemini/antigravity-cli and ~/.gemini/config — so the answer is derived
// from the same root the provider resolves, never guessed independently. The
// adapter's own override outranks both.
func AgyConfigDir() string {
	if dir := os.Getenv(AgyConfigDirEnv); dir != "" {
		return config.ExpandPath(dir)
	}
	root := config.EnvRootOrDefault("AGY_HOME", filepath.Join(config.HomeDir(), ".gemini", "antigravity-cli"))
	return filepath.Join(filepath.Dir(root), "config")
}

// AgyHooksFile is the shared hook document inside a resolved config directory.
func AgyHooksFile(configDir string) string {
	return filepath.Join(configDir, "hooks.json")
}

// AgyCommand is the hook command as this binary installs it. It is the whole
// contract: whatever the group holds instead is not what another wrote.
const AgyCommand = "another rename --from agy --auto --skip-conforming --allow-current --refresh --hook-stdin"

// hookDoc is the shared hooks.json: a group name to a map of events, each
// event a list of hook specifications. Foreign groups are preserved as raw
// messages so a shape another does not know about still round-trips.
type hookDoc map[string]map[string][]hookSpec

type hookSpec struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// AgyStatus reads the shared hooks.json and reports what it holds for this
// adapter. A missing group is missing; a group with this binary's command is
// current; anything else under that name was not written by another.
func AgyStatus(configDir string) Status {
	file := AgyHooksFile(configDir)
	status := Status{ConfigDir: configDir, Dir: file, State: StateMissing}
	data, err := os.ReadFile(file)
	if err != nil {
		return status
	}
	var doc hookDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		// A file another cannot parse is not another's installation.
		status.State = StateForeign
		return status
	}
	group, ok := doc[AgyGroup]
	if !ok {
		return status
	}
	for _, spec := range group["Stop"] {
		if spec.Command == AgyCommand {
			status.State = StateCurrent
			return status
		}
	}
	status.State = StateForeign
	return status
}

// InstallAgy merges the hook into the shared hooks.json under the same rules
// as every other adapter: a group another did not write is left alone unless
// the caller says otherwise.
func InstallAgy(status Status, version, language string, force bool) (Status, error) {
	if status.State.Blocked() && !force {
		return status, fmt.Errorf("%s already holds a hook another did not write; remove it by hand or rerun with --force", util.TildePath(status.Dir))
	}
	// The file may belong to other tools; read what is there and keep it.
	doc := hookDoc{}
	mode := os.FileMode(0o644)
	if data, err := os.ReadFile(status.Dir); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			// Merging means rewriting the whole file, and a document
			// another cannot parse holds groups another cannot carry
			// over. No flag makes that loss safe to do silently.
			return status, fmt.Errorf("%s is not a hook document another can extend; move it aside or edit it by hand", util.TildePath(status.Dir))
		}
		if info, err := os.Stat(status.Dir); err == nil {
			mode = info.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return status, fmt.Errorf("read %s: %w", util.TildePath(status.Dir), err)
	}
	doc[AgyGroup] = map[string][]hookSpec{
		"Stop": {{Command: AgyCommand, Timeout: 180}},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return status, fmt.Errorf("write %s: %w", util.TildePath(status.Dir), err)
	}
	if err := os.WriteFile(status.Dir, append(data, '\n'), mode); err != nil {
		return status, fmt.Errorf("write %s: %w", util.TildePath(status.Dir), err)
	}
	return AgyStatus(status.ConfigDir), nil
}

// RemoveAgy takes back only the group another wrote; every foreign group
// stays exactly as it was.
func RemoveAgy(status Status, force bool) error {
	if !status.State.Installed() {
		return nil
	}
	if status.State.Blocked() && !force {
		return fmt.Errorf("%s holds a hook another did not write; remove it by hand or rerun with --force", util.TildePath(status.Dir))
	}
	data, err := os.ReadFile(status.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", util.TildePath(status.Dir), err)
	}
	var doc hookDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s is not a hook document another can edit: %w", util.TildePath(status.Dir), err)
	}
	delete(doc, AgyGroup)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("write %s: %w", util.TildePath(status.Dir), err)
	}
	if err := os.WriteFile(status.Dir, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", util.TildePath(status.Dir), err)
	}
	return nil
}
