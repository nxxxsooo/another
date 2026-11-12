// Package opencode2sessions embeds the tested /ref TUI plugin.
package opencode2sessions

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"sort"
)

const PluginID = "another.sessions"
const DirName = "another-sessions"

//go:embed src/index.ts
var indexTS []byte

//go:embed src/tui.ts
var tuiTS []byte

//go:embed src/command.ts
var commandTS []byte

func Files() map[string][]byte {
	return map[string][]byte{"index.ts": indexTS, "tui.ts": tuiTS, "command.ts": commandTS}
}
func Names() []string {
	names := []string{}
	for name := range Files() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func Hash(content []byte) string { sum := sha256.Sum256(content); return hex.EncodeToString(sum[:]) }
