// Package anotherskill carries the `another` skill inside the another binary.
// Agents that list skills in their @ picker (OpenCode, Codex) offer it as
// @another; selecting it loads instructions for finding and reading a past
// session through another's CLI or MCP server.
package anotherskill

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

// DirName is the skill's directory, which is also its ID: @another.
const DirName = "another"

//go:embed SKILL.md
var skillMD []byte

// Files is the skill as it must appear on disk.
func Files() map[string][]byte {
	return map[string][]byte{"SKILL.md": skillMD}
}

// Names lists the installed files in a stable order.
func Names() []string { return []string{"SKILL.md"} }

// Hashes reduces the shipped skill to what a comparison needs.
func Hashes() map[string]string {
	return map[string]string{"SKILL.md": Hash(skillMD)}
}

func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
