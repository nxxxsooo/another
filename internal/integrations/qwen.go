package integrations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/util"
)

// Qwen is the title-policy hook adapter. Qwen Code already names its
// conversations itself, so this adapter does not add a second naming
// pipeline — it adds the shared policy one: at the end of an idle turn, the
// hook asks another to rename the conversation, and everything the policy
// needs (the title agent, the language, the validation) stays in the binary.
//
// The hook is an entry in Qwen Code's shared settings.json under the Stop
// event rather than an extension. The shared file is the surface other tools
// already use successfully: entries coexist, each keeping its own command.
const Qwen = "qwen-title-policy"

// QwenName is the name of the hook spec inside the Stop event. It is the
// identity contract: status and removal find the installation by it and by
// the command line.
const QwenName = "another-title-policy"

// QwenConfigDirEnv overrides directory resolution entirely, for tests and for
// a Qwen Code whose layout another cannot find.
const QwenConfigDirEnv = "ANOTHER_QWEN_CONFIG_DIR"

// QwenConfigDir resolves the directory holding the settings.json Qwen Code reads.
//
// Qwen Code reads ~/.qwen/settings.json by default, or $QWEN_HOME/settings.json,
// so the answer is derived from the same root the provider resolves. The
// adapter's own override outranks both.
func QwenConfigDir() string {
	if dir := os.Getenv(QwenConfigDirEnv); dir != "" {
		return config.ExpandPath(dir)
	}
	return config.EnvRootOrDefault("QWEN_HOME", filepath.Join(config.HomeDir(), ".qwen"))
}

// QwenSettingsFile is the shared settings document inside a resolved config directory.
func QwenSettingsFile(configDir string) string {
	return filepath.Join(configDir, "settings.json")
}

// QwenCommand is the hook command as this binary installs it. It is the whole
// contract: whatever the hook holds instead is not what another wrote.
const QwenCommand = "another rename --from qwen --auto --skip-conforming --allow-current --refresh --hook-stdin"

// QwenTimeoutSec is the timeout in seconds for the hook execution.
const QwenTimeoutSec = 180

type qwenHookSpec struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Name    string `json:"name,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

type qwenMatcherEntry struct {
	Matcher string         `json:"matcher,omitempty"`
	Hooks   []qwenHookSpec `json:"hooks"`
}

// QwenStatus reads the shared settings.json and reports what it holds for this
// adapter. A missing hook is missing; a hook with this binary's command is
// current; anything else under that name was not written by another.
func QwenStatus(configDir string) Status {
	file := QwenSettingsFile(configDir)
	status := Status{ConfigDir: configDir, Dir: file, State: StateMissing}
	data, err := os.ReadFile(file)
	if err != nil {
		return status
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		status.State = StateForeign
		return status
	}
	hooksRaw, ok := root["hooks"]
	if !ok {
		return status
	}
	var hooksMap map[string]json.RawMessage
	if err := json.Unmarshal(hooksRaw, &hooksMap); err != nil {
		status.State = StateForeign
		return status
	}
	stopRaw, ok := hooksMap["Stop"]
	if !ok {
		return status
	}
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(stopRaw, &rawEntries); err != nil {
		status.State = StateForeign
		return status
	}
	for _, r := range rawEntries {
		var entry qwenMatcherEntry
		if err := json.Unmarshal(r, &entry); err == nil {
			for _, h := range entry.Hooks {
				if h.Command == QwenCommand {
					status.State = StateCurrent
					return status
				}
				if h.Name == QwenName {
					status.State = StateForeign
					return status
				}
			}
		}
	}
	return status
}

// InstallQwen merges the hook into the shared settings.json under the same rules
// as every other adapter: a hook another did not write is left alone unless
// the caller says otherwise. Every other setting and hook keeps its value; the
// file is rewritten as indented JSON with sorted keys.
func InstallQwen(status Status, version, language string, force bool) (Status, error) {
	if status.State.Blocked() && !force {
		return status, fmt.Errorf("%s already holds a hook another did not write; remove it by hand or rerun with --force", util.TildePath(status.Dir))
	}
	// The file may belong to other tools; read what is there and keep it.
	root := make(map[string]json.RawMessage)
	mode := os.FileMode(0o644)
	if data, err := os.ReadFile(status.Dir); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return status, fmt.Errorf("%s is not a hook document another can extend; move it aside or edit it by hand", util.TildePath(status.Dir))
		}
		if info, err := os.Stat(status.Dir); err == nil {
			mode = info.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return status, fmt.Errorf("read %s: %w", util.TildePath(status.Dir), err)
	}

	hooksDoc := make(map[string]json.RawMessage)
	if raw, ok := root["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooksDoc); err != nil {
			return status, fmt.Errorf("%s is not a hook document another can extend; move it aside or edit it by hand", util.TildePath(status.Dir))
		}
	}

	var finalStopEntries []json.RawMessage
	if raw, ok := hooksDoc["Stop"]; ok {
		var rawEntries []json.RawMessage
		if err := json.Unmarshal(raw, &rawEntries); err != nil {
			// Replacing a Stop value another cannot read would drop whatever
			// the person put there.
			return status, fmt.Errorf("%s holds a Stop hook value another cannot extend; edit it by hand", util.TildePath(status.Dir))
		}
		{
			for _, r := range rawEntries {
				var entry qwenMatcherEntry
				if err := json.Unmarshal(r, &entry); err == nil {
					hasAnother := false
					for _, h := range entry.Hooks {
						if h.Name == QwenName || h.Command == QwenCommand {
							hasAnother = true
							break
						}
					}
					if !hasAnother {
						finalStopEntries = append(finalStopEntries, r)
					}
				} else {
					finalStopEntries = append(finalStopEntries, r)
				}
			}
		}
	}

	ourEntry := qwenMatcherEntry{
		Hooks: []qwenHookSpec{
			{
				Type:    "command",
				Command: QwenCommand,
				Name:    QwenName,
				Timeout: QwenTimeoutSec,
			},
		},
	}
	ourEntryBytes, err := json.Marshal(ourEntry)
	if err != nil {
		return status, fmt.Errorf("marshal qwen hook: %w", err)
	}
	finalStopEntries = append(finalStopEntries, json.RawMessage(ourEntryBytes))

	stopBytes, err := json.Marshal(finalStopEntries)
	if err != nil {
		return status, fmt.Errorf("marshal stop hooks: %w", err)
	}
	hooksDoc["Stop"] = json.RawMessage(stopBytes)

	hooksBytes, err := json.Marshal(hooksDoc)
	if err != nil {
		return status, fmt.Errorf("marshal hooks: %w", err)
	}
	root["hooks"] = json.RawMessage(hooksBytes)

	if err := os.MkdirAll(status.ConfigDir, 0o700); err != nil {
		return status, fmt.Errorf("create config dir %s: %w", util.TildePath(status.ConfigDir), err)
	}

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return status, fmt.Errorf("write %s: %w", util.TildePath(status.Dir), err)
	}
	if err := util.WriteFileAtomic(status.Dir, append(data, '\n'), mode); err != nil {
		return status, fmt.Errorf("write %s: %w", util.TildePath(status.Dir), err)
	}
	return QwenStatus(status.ConfigDir), nil
}

// RemoveQwen takes back only the hook another wrote; every foreign hook and
// setting stays exactly as it was.
func RemoveQwen(status Status, force bool) error {
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
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("%s is not a hook document another can edit: %w", util.TildePath(status.Dir), err)
	}
	hooksRaw, ok := root["hooks"]
	if !ok {
		return nil
	}
	var hooksDoc map[string]json.RawMessage
	if err := json.Unmarshal(hooksRaw, &hooksDoc); err != nil {
		return fmt.Errorf("%s has invalid hooks configuration: %w", util.TildePath(status.Dir), err)
	}
	stopRaw, ok := hooksDoc["Stop"]
	if !ok {
		return nil
	}
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(stopRaw, &rawEntries); err != nil {
		return fmt.Errorf("%s has invalid Stop hooks: %w", util.TildePath(status.Dir), err)
	}
	var finalStopEntries []json.RawMessage
	for _, r := range rawEntries {
		kept, changed := withoutQwenHook(r)
		switch {
		case !changed:
			finalStopEntries = append(finalStopEntries, r)
		case kept != nil:
			finalStopEntries = append(finalStopEntries, kept)
		}
	}
	if len(finalStopEntries) == 0 {
		delete(hooksDoc, "Stop")
	} else {
		cleanedBytes, err := json.Marshal(finalStopEntries)
		if err != nil {
			return fmt.Errorf("marshal cleaned stop hooks: %w", err)
		}
		hooksDoc["Stop"] = json.RawMessage(cleanedBytes)
	}

	if len(hooksDoc) == 0 {
		delete(root, "hooks")
	} else {
		hooksBytes, err := json.Marshal(hooksDoc)
		if err != nil {
			return fmt.Errorf("marshal hooks: %w", err)
		}
		root["hooks"] = json.RawMessage(hooksBytes)
	}

	mode := os.FileMode(0o644)
	if info, err := os.Stat(status.Dir); err == nil {
		mode = info.Mode().Perm()
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("write %s: %w", util.TildePath(status.Dir), err)
	}
	if err := util.WriteFileAtomic(status.Dir, append(out, '\n'), mode); err != nil {
		return fmt.Errorf("write %s: %w", util.TildePath(status.Dir), err)
	}
	return nil
}

// withoutQwenHook drops another's hook from one Stop entry. Every other hook
// and field is carried over as raw JSON, so a field another does not model is
// never lost. changed is false when the entry holds no hook of another's; kept
// is nil when another's hook was all the entry held.
func withoutQwenHook(raw json.RawMessage) (kept json.RawMessage, changed bool) {
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, false
	}
	var hooks []json.RawMessage
	if err := json.Unmarshal(entry["hooks"], &hooks); err != nil {
		return nil, false
	}
	var rest []json.RawMessage
	for _, h := range hooks {
		var spec qwenHookSpec
		if json.Unmarshal(h, &spec) == nil && (spec.Name == QwenName || spec.Command == QwenCommand) {
			changed = true
			continue
		}
		rest = append(rest, h)
	}
	if !changed || len(rest) == 0 {
		return nil, changed
	}
	b, err := json.Marshal(rest)
	if err != nil {
		return nil, false
	}
	entry["hooks"] = b
	out, err := json.Marshal(entry)
	if err != nil {
		return nil, false
	}
	return out, true
}
