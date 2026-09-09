// Package pititle carries the Pi title-policy extension inside the another
// binary, for the same reason the OpenCode 2 plugin is carried here: go:embed
// cannot reach outside its own directory, so the Go package lives beside the
// TypeScript it ships rather than under internal/, where the repository would
// need a second copy that drifts from the one the tests cover.
package pititle

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"sort"
)

// DirName is the directory another installs into, under Pi's extensions
// directory. Pi discovers an extension by its default export, so no manifest
// of Pi's own is needed.
const DirName = "another-title-policy"

//go:embed src/index.ts
var indexTS []byte

// Files is the extension as it must appear on disk, keyed by the path each
// file takes inside the installed directory.
func Files() map[string][]byte {
	return map[string][]byte{"index.ts": indexTS}
}

// Names lists the installed files in a stable order, so writing and reporting
// them does not depend on map iteration.
func Names() []string {
	files := Files()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Hashes reduces the shipped extension to what a comparison needs: the same
// file written twice hashes the same, and a file a person has edited does not.
func Hashes() map[string]string {
	hashes := make(map[string]string, len(Files()))
	for name, content := range Files() {
		hashes[name] = Hash(content)
	}
	return hashes
}

func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
