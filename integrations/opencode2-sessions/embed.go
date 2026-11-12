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

//go:embed src/comment.tsx
var commentTSX []byte

//go:embed src/server.ts
var serverTS []byte

//go:embed src/watch.ts
var watchTS []byte

//go:embed src/package.json
var packageJSON []byte

func Files() map[string][]byte {
	return map[string][]byte{"package.json": packageJSON, "index.ts": indexTS, "tui.ts": tuiTS, "command.ts": commandTS, "comment.tsx": commentTSX, "server.ts": serverTS, "watch.ts": watchTS}
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
