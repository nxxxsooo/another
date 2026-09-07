// Package opencode2title carries the OpenCode 2 title-policy plugin inside the
// another binary. The Go package lives beside the TypeScript it embeds rather
// than under internal/ because go:embed cannot reach outside its own directory:
// a package elsewhere would need a second copy of the plugin in this repository,
// and the copy another shipped would drift from the copy its tests cover.
package opencode2title

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"sort"
)

// PluginID is the id the plugin reports to OpenCode 2. another uses it to
// recognise its own plugin in an OpenCode 2 configuration it did not write.
const PluginID = "another.title-policy"

// DirName is the directory another installs into, under OpenCode 2's global
// plugins directory. OpenCode 2 discovers such a directory through its
// index.ts entrypoint, so no package manifest is needed.
const DirName = "another-title-policy"

//go:embed src/index.ts
var indexTS []byte

//go:embed src/policy.ts
var policyTS []byte

// Files is the plugin as it must appear on disk, keyed by the path each file
// takes inside the installed directory. The test files and the npm project
// around them stay in the repository and out of the binary.
func Files() map[string][]byte {
	return map[string][]byte{
		"index.ts":  indexTS,
		"policy.ts": policyTS,
	}
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

// Hashes is the content of the shipped plugin reduced to what a comparison
// needs: the same file written twice hashes the same, and a file a person has
// edited does not.
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
