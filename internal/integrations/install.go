package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nxxxsooo/another/internal/util"
)

// InstallOpenCode2 writes the shipped plugin into the resolved OpenCode 2
// configuration and records what it wrote. It is safe to call when the plugin
// is already current: that call writes nothing and reports the same status
// back. It refuses to overwrite files another did not write unless it is told
// to, because those files are someone's work.
func InstallOpenCode2(status Status, version, language string, force bool) (Status, error) {
	if err := installBundle(status, opencode2Bundle, version, language, force); err != nil {
		return status, err
	}
	return OpenCode2Status(status.ConfigDir), nil
}

// installBundle writes one adapter's files and its manifest. Every adapter
// shares it, so drift detection cannot diverge between them.
func installBundle(status Status, b bundle, version, language string, force bool) error {
	if status.State.Blocked() && !force {
		return fmt.Errorf("%s already holds files another did not write; move them aside or rerun with --force", util.TildePath(status.Dir))
	}
	if err := os.MkdirAll(status.Dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", util.TildePath(status.Dir), err)
	}
	// A language change is invisible to a plugin that is already loaded: it
	// reads the language once, when OpenCode 2 starts it. Rewriting the files
	// even when their content is identical is what makes OpenCode 2's watcher
	// reload the plugin, so the language chosen in setup takes effect without
	// anyone restarting a service.
	reload := status.State.Installed() && status.Language != language
	for _, name := range b.Names() {
		path := filepath.Join(status.Dir, name)
		content := b.Files()[name]
		if !reload && sameContent(path, content) {
			continue
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", util.TildePath(path), err)
		}
	}
	saved := manifest{
		Integration: b.Integration,
		Plugin:      b.Plugin,
		Version:     version,
		Language:    language,
		Files:       b.Hashes(),
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("record installation: %w", err)
	}
	path := filepath.Join(status.Dir, manifestName)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", util.TildePath(path), err)
	}
	return nil
}

// RemoveOpenCode2 takes back only what another installed: the files it ships
// and its own manifest, and the directory when nothing else is left in it.
func RemoveOpenCode2(status Status, force bool) error {
	return removeBundle(status, opencode2Bundle, force)
}

// removeBundle takes back only what another installed: the files it ships and
// its own manifest, and the directory when nothing else is left in it.
func removeBundle(status Status, b bundle, force bool) error {
	if !status.State.Installed() {
		return nil
	}
	if status.State.Blocked() && !force {
		return fmt.Errorf("%s holds files another did not write; remove it by hand or rerun with --force", util.TildePath(status.Dir))
	}
	for _, name := range append(b.Names(), manifestName) {
		path := filepath.Join(status.Dir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", util.TildePath(path), err)
		}
	}
	// A directory someone else has added to is theirs to keep.
	entries, err := os.ReadDir(status.Dir)
	if err != nil || len(entries) > 0 {
		return nil
	}
	if err := os.Remove(status.Dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", util.TildePath(status.Dir), err)
	}
	return nil
}

func sameContent(path string, content []byte) bool {
	existing, err := os.ReadFile(path)
	return err == nil && bytes.Equal(existing, content)
}
