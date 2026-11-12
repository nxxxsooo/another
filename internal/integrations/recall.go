package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	skill "github.com/nxxxsooo/another/integrations/another-skill"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/util"
	"github.com/tailscale/hujson"
)

// Kind separates what an adapter is for. Title adapters carry another's naming
// policy into an agent; recall adapters let that agent read past sessions. An
// empty Kind is a title adapter, which is every adapter written before recall.
type Kind string

const (
	KindTitle  Kind = "title"
	KindRecall Kind = "recall"
)

// Of reports the adapter's kind.
func (a Adapter) Of() Kind {
	if a.Kind == "" {
		return KindTitle
	}
	return a.Kind
}

// Recall adapters register `another mcp` with an agent, which is what puts
// this project's sessions into the agent's own @ picker.
const (
	ClaudeCodeRecall = "claude-code-recall"
	OpenCodeRecall   = "opencode-recall"
	QwenRecall       = "qwen-recall"
)

// MCPServerName is the key the server is registered under in every agent. It
// is the identity contract: status and removal find the entry by it.
const MCPServerName = "another"

var recallAdapters = []Adapter{
	{
		ID:        ClaudeCodeRecall,
		Kind:      KindRecall,
		Provider:  "claude-code",
		ConfigDir: func(context.Context) string { return ClaudeConfigDir() },
		Status:    ClaudeRecallStatus,
		Install:   InstallClaudeRecall,
		Remove:    RemoveClaudeRecall,
		Consent:   func(i *config.Integrations) *bool { return &i.ClaudeCodeRecall },
	},
	{
		ID:        OpenCodeRecall,
		Kind:      KindRecall,
		Provider:  "opencode",
		ConfigDir: OpenCode2ConfigDir,
		Status:    OpenCodeRecallStatus,
		Install:   InstallOpenCodeRecall,
		Remove:    RemoveOpenCodeRecall,
		Consent:   func(i *config.Integrations) *bool { return &i.OpenCodeRecall },
	},
	{
		ID:        QwenRecall,
		Kind:      KindRecall,
		Provider:  "qwen",
		ConfigDir: func(context.Context) string { return QwenConfigDir() },
		Status:    QwenRecallStatus,
		Install:   InstallQwenRecall,
		Remove:    RemoveQwenRecall,
		Consent:   func(i *config.Integrations) *bool { return &i.QwenRecall },
	},
}

// RecallForProvider returns the recall adapter for this agent, if there is one.
func RecallForProvider(provider string) (Adapter, bool) {
	switch provider {
	case "opencode2", "open-code-2", "o2":
		provider = "opencode"
	case "qwen-code", "qwen_code":
		provider = "qwen"
	}
	for _, a := range recallAdapters {
		if a.Provider == provider {
			return a, true
		}
	}
	return Adapter{}, false
}

// RecallProviders lists the agents that have a recall adapter.
func RecallProviders() []string {
	names := make([]string, 0, len(recallAdapters))
	for _, a := range recallAdapters {
		names = append(names, a.Provider)
	}
	return names
}

// MCPCommand is the command an agent runs to reach another's MCP server: the
// binary that installs it, by name, so a development build registers itself
// and a released one does too. A name another does not recognize as its own —
// a test binary — falls back to the released name.
func MCPCommand() []string {
	name := "another"
	if exe, err := os.Executable(); err == nil {
		base := filepath.Base(exe)
		if runtime.GOOS == "windows" {
			base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".EXE")
		}
		if strings.HasPrefix(base, "another") {
			name = base
		}
	}
	return []string{name, "mcp"}
}

// recallState judges a registered server against what this binary would
// register. One whose program is another's under a different name — a
// development build next to a release — is another's own and only outdated;
// anything else under the name is not another's to replace.
func recallState(command []string) State {
	want := MCPCommand()
	if slices.Equal(command, want) {
		return StateCurrent
	}
	if len(command) == 2 && command[1] == "mcp" && strings.HasPrefix(filepath.Base(command[0]), "another") {
		return StateOutdated
	}
	return StateForeign
}

// ClaudeConfigDirEnv overrides directory resolution, for tests.
const ClaudeConfigDirEnv = "ANOTHER_CLAUDE_CONFIG_DIR"

// ClaudeConfigDir is the directory holding the .claude.json Claude Code keeps
// its user-scope MCP servers in: CLAUDE_CONFIG_DIR when set, home otherwise.
func ClaudeConfigDir() string {
	if dir := os.Getenv(ClaudeConfigDirEnv); dir != "" {
		return config.ExpandPath(dir)
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return config.ExpandPath(dir)
	}
	return config.HomeDir()
}

// ClaudeStateFile is the document inside a resolved directory.
func ClaudeStateFile(configDir string) string {
	return filepath.Join(configDir, ".claude.json")
}

// ClaudeStatus reads the registered server without asking Claude Code: the
// file is Claude Code's own and is only ever read here.
func ClaudeRecallStatus(configDir string) Status {
	file := ClaudeStateFile(configDir)
	status := Status{ConfigDir: configDir, Dir: file, State: StateMissing}
	data, err := os.ReadFile(file)
	if err != nil {
		return status
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		status.State = StateForeign
		return status
	}
	entry, ok := doc.MCPServers[MCPServerName]
	if !ok {
		return status
	}
	status.State = recallState(append([]string{entry.Command}, entry.Args...))
	return status
}

// runClaude runs the Claude Code CLI against one configuration directory.
// .claude.json is rewritten by every running Claude Code session, so another
// leaves the write to Claude Code's own command rather than racing it. Tests
// replace this.
var runClaude = func(configDir string, args ...string) error {
	command := config.EnvOrDefault("CLAUDE_COMMAND", "claude")
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("the Claude Code CLI (%s) is not on PATH", command)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	if home := config.HomeDir(); util.NormalizeProjectPath(configDir) != util.NormalizeProjectPath(home) {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	cmd.Dir = config.HomeDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("claude %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// InstallClaudeRecall registers the server at user scope, so it follows the
// person into every project and reads whichever project Claude Code starts in.
func InstallClaudeRecall(status Status, _, _ string, force bool) (Status, error) {
	if status.State == StateCurrent {
		return status, nil
	}
	if status.State.Blocked() && !force {
		return status, fmt.Errorf("the Claude Code MCP server %q was not registered by another; remove it or rerun with --force", MCPServerName)
	}
	if status.State.Installed() {
		if err := runClaude(status.ConfigDir, "mcp", "remove", "--scope", "user", MCPServerName); err != nil {
			return status, err
		}
	}
	args := append([]string{"mcp", "add", "--scope", "user", MCPServerName, "--"}, MCPCommand()...)
	if err := runClaude(status.ConfigDir, args...); err != nil {
		return status, err
	}
	return ClaudeRecallStatus(status.ConfigDir), nil
}

// RemoveClaudeRecall unregisters only the server another registered.
func RemoveClaudeRecall(status Status, force bool) error {
	if !status.State.Installed() {
		return nil
	}
	if status.State.Blocked() && !force {
		return fmt.Errorf("the Claude Code MCP server %q was not registered by another; remove it by hand or rerun with --force", MCPServerName)
	}
	return runClaude(status.ConfigDir, "mcp", "remove", "--scope", "user", MCPServerName)
}

// openCodeConfigFile is the global document another edits: the JSONC file
// when there is one, then the JSON one, and a new JSON file when neither
// exists yet.
func openCodeConfigFile(configDir string) string {
	for _, name := range []string{"opencode.jsonc", "opencode.json"} {
		path := filepath.Join(configDir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join(configDir, "opencode.json")
}

type openCodeMCP struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
}

// OpenCode 2 keeps servers under mcp.servers; a server directly under mcp is
// the deprecated V1 form it still reads. Either is a server by that name.
var (
	openCodeServerPath = []string{"mcp", "servers", MCPServerName}
	openCodeLegacyPath = []string{"mcp", MCPServerName}
)

// OpenCode 2's @ picker lists skills but not MCP resources, so the recall
// adapter installs both: the MCP server, which the agent reads sessions
// through, and the `another` skill, which is what @another selects.

// OpenCodeSkillDir is where the skill belongs inside a resolved configuration
// directory; OpenCode 2 discovers skills/ on its own.
func OpenCodeSkillDir(configDir string) string {
	return filepath.Join(configDir, "skills", skill.DirName)
}

var skillBundle = bundle{
	Integration: "another-skill",
	Names:       skill.Names,
	Files:       skill.Files,
	Hashes:      skill.Hashes,
	Hash:        skill.Hash,
}

// skillStatus compares an installed copy of the skill with the one this
// binary ships, by the same rules as every other bundle.
func skillStatus(dir string) Status {
	status := Status{Dir: dir, State: StateMissing}
	installed, err := readInstalled(dir, skillBundle)
	if err != nil || len(installed) == 0 {
		return status
	}
	current := sameHashes(installed, skillBundle.Hashes())
	saved, ok := readManifest(dir, skillBundle)
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

// combineStates reports two parts of one adapter as a single state: anything
// another must not overwrite wins, agreement is reported as is, and a partial
// installation is outdated, which is what makes install complete it.
func combineStates(a, b State) State {
	switch {
	case a.Blocked():
		return a
	case b.Blocked():
		return b
	case a == b:
		return a
	default:
		return StateOutdated
	}
}

// OpenCodeRecallStatus reports the MCP server and the skill together.
func OpenCodeRecallStatus(configDir string) Status {
	status := openCodeMCPStatus(configDir)
	status.State = combineStates(status.State, skillStatus(OpenCodeSkillDir(configDir)).State)
	status.State = combineStates(status.State, OpenCodeSessionsStatus(configDir).State)
	return status
}

// openCodeMCPStatus reads the registered server from the global
// configuration, in either form OpenCode 2 accepts.
func openCodeMCPStatus(configDir string) Status {
	file := openCodeConfigFile(configDir)
	status := Status{ConfigDir: configDir, Dir: file, State: StateMissing}
	data, err := os.ReadFile(file)
	if err != nil {
		return status
	}
	std, err := hujson.Standardize(data)
	if err != nil {
		status.State = StateForeign
		return status
	}
	var doc struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	if err := json.Unmarshal(std, &doc); err != nil {
		status.State = StateForeign
		return status
	}
	var servers map[string]json.RawMessage
	if raw, ok := doc.MCP["servers"]; ok {
		_ = json.Unmarshal(raw, &servers)
	}
	raw, ok := servers[MCPServerName]
	if !ok {
		if raw, ok = doc.MCP[MCPServerName]; !ok {
			return status
		}
	}
	var entry openCodeMCP
	if err := json.Unmarshal(raw, &entry); err != nil || entry.Type != "local" {
		status.State = StateForeign
		return status
	}
	status.State = recallState(entry.Command)
	return status
}

// InstallOpenCodeRecall adds mcp.servers.another to the user's global
// configuration and writes the skill beside it.
// The file is JSONC that people annotate, so it is patched in place: comments,
// order, and every other entry stay byte for byte, and the one entry another
// adds is the one entry removal takes back.
func InstallOpenCodeRecall(status Status, version, _ string, force bool) (Status, error) {
	if status.State == StateCurrent {
		return status, nil
	}
	if status.State.Blocked() && !force {
		return status, fmt.Errorf("%s already has an MCP server named %q that another did not add; remove it or rerun with --force", util.TildePath(status.Dir), MCPServerName)
	}
	if openCodeMCPStatus(status.ConfigDir).State != StateCurrent {
		err := patchJSONC(status.Dir, func(root *hujson.Object, nl string) error {
			removeMember(root, openCodeLegacyPath)
			return setMember(root, nl, openCodeServerPath, openCodeMCP{Type: "local", Command: MCPCommand()})
		})
		if err != nil {
			return status, err
		}
	}
	if skilled := skillStatus(OpenCodeSkillDir(status.ConfigDir)); skilled.State != StateCurrent {
		if err := installBundle(skilled, skillBundle, version, "", force); err != nil {
			return status, err
		}
	}
	if _, err := InstallOpenCodeSessions(OpenCodeSessionsStatus(status.ConfigDir), version, "", force); err != nil {
		return status, err
	}
	return OpenCodeRecallStatus(status.ConfigDir), nil
}

// RemoveOpenCodeRecall takes back the server and the skill another added and
// nothing else.
func RemoveOpenCodeRecall(status Status, force bool) error {
	if !status.State.Installed() {
		return nil
	}
	if status.State.Blocked() && !force {
		return fmt.Errorf("%s holds an MCP server or skill named %q that another did not add; remove it by hand or rerun with --force", util.TildePath(status.Dir), MCPServerName)
	}
	if openCodeMCPStatus(status.ConfigDir).State.Installed() {
		err := patchJSONC(status.Dir, func(root *hujson.Object, _ string) error {
			removeMember(root, openCodeServerPath)
			removeMember(root, openCodeLegacyPath)
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err := removeBundle(skillStatus(OpenCodeSkillDir(status.ConfigDir)), skillBundle, force); err != nil {
		return err
	}
	return RemoveOpenCodeSessions(OpenCodeSessionsStatus(status.ConfigDir), force)
}

// patchJSONC applies edit to the root object of a JSONC document and writes it
// back atomically with its permissions. A missing file starts as an empty
// object; a file that does not parse is refused, because rewriting it would
// lose what it holds.
func patchJSONC(file string, edit func(root *hujson.Object, newline string) error) error {
	data, err := os.ReadFile(file)
	mode := os.FileMode(0o644)
	switch {
	case os.IsNotExist(err):
		data = []byte("{}\n")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("read %s: %w", util.TildePath(file), err)
	default:
		if info, err := os.Stat(file); err == nil {
			mode = info.Mode().Perm()
		}
	}
	v, err := hujson.Parse(data)
	if err != nil {
		return fmt.Errorf("%s is not a configuration another can extend; fix or edit it by hand: %w", util.TildePath(file), err)
	}
	root, ok := v.Value.(*hujson.Object)
	if !ok {
		return fmt.Errorf("%s does not hold a JSON object", util.TildePath(file))
	}
	newline := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		newline = "\r\n"
	}
	if err := edit(root, newline); err != nil {
		return fmt.Errorf("edit %s: %w", util.TildePath(file), err)
	}
	if err := util.WriteFileAtomic(file, v.Pack(), mode); err != nil {
		return fmt.Errorf("write %s: %w", util.TildePath(file), err)
	}
	return nil
}

// setMember sets the member at path to value, creating objects on the way.
// A new member is written the way its siblings already are — their
// indentation, their line ending, their trailing comma or lack of one — so
// the file reads as if a person had added it.
func setMember(obj *hujson.Object, newline string, path []string, value any) error {
	indent := memberIndent(obj, "", "  ")
	// The file's own indentation step, read off the top level, so a file
	// indented by four is extended by four.
	step := indent
	for depth, name := range path {
		last := depth == len(path)-1
		i := memberIndex(obj, name)
		if i >= 0 && !last {
			next, ok := obj.Members[i].Value.Value.(*hujson.Object)
			if !ok {
				return fmt.Errorf("%q is not an object", name)
			}
			indent = memberIndent(next, indent, step)
			obj = next
			continue
		}
		nested := value
		for j := len(path) - 1; j > depth; j-- {
			nested = map[string]any{path[j]: nested}
		}
		body, err := json.MarshalIndent(nested, indent, step)
		if err != nil {
			return err
		}
		parsed, err := hujson.Parse([]byte(strings.ReplaceAll(string(body), "\n", newline)))
		if err != nil {
			return err
		}
		parsed.BeforeExtra = hujson.Extra(" ")
		if i >= 0 {
			parsed.AfterExtra = obj.Members[i].Value.AfterExtra
			obj.Members[i].Value = parsed
			return nil
		}
		key, _ := json.Marshal(name)
		member := hujson.ObjectMember{
			Name:  hujson.Value{BeforeExtra: hujson.Extra(newline + indent), Value: hujson.Literal(key)},
			Value: parsed,
		}
		if n := len(obj.Members); n > 0 && obj.Members[n-1].Value.AfterExtra != nil {
			member.Value.AfterExtra = hujson.Extra{}
		} else if n == 0 {
			closing := strings.TrimSuffix(indent, step)
			obj.AfterExtra = hujson.Extra(newline + closing)
		}
		obj.Members = append(obj.Members, member)
		return nil
	}
	return nil
}

// removeMember deletes the member at path. An object it leaves empty and
// uncommented goes too, so removing what setMember created restores the file.
func removeMember(obj *hujson.Object, path []string) {
	parents := []*hujson.Object{obj}
	for _, name := range path[:len(path)-1] {
		i := memberIndex(obj, name)
		if i < 0 {
			return
		}
		next, ok := obj.Members[i].Value.Value.(*hujson.Object)
		if !ok {
			return
		}
		obj = next
		parents = append(parents, obj)
	}
	name := path[len(path)-1]
	for depth := len(parents) - 1; depth >= 0; depth-- {
		obj := parents[depth]
		i := memberIndex(obj, name)
		if i < 0 {
			return
		}
		if child, ok := obj.Members[i].Value.Value.(*hujson.Object); ok && name != path[len(path)-1] {
			if len(child.Members) > 0 || strings.TrimSpace(string(child.AfterExtra)) != "" {
				return
			}
		}
		trailing := obj.Members[len(obj.Members)-1].Value.AfterExtra != nil
		obj.Members = append(obj.Members[:i], obj.Members[i+1:]...)
		if len(obj.Members) == 0 && strings.TrimSpace(string(obj.AfterExtra)) == "" {
			obj.AfterExtra = nil
		}
		if n := len(obj.Members); n > 0 && trailing && obj.Members[n-1].Value.AfterExtra == nil {
			obj.Members[n-1].Value.AfterExtra = hujson.Extra{}
		}
		if depth > 0 {
			name = path[depth-1]
		}
	}
}

func memberIndex(obj *hujson.Object, name string) int {
	for i, m := range obj.Members {
		if lit, ok := m.Name.Value.(hujson.Literal); ok && lit.String() == name {
			return i
		}
	}
	return -1
}

// memberIndent is the whitespace that starts a line holding one of obj's
// members: what its first member uses, or one step past its parent's.
func memberIndent(obj *hujson.Object, parent, step string) string {
	if len(obj.Members) > 0 {
		before := string(obj.Members[0].Name.BeforeExtra)
		if i := strings.LastIndexAny(before, "\r\n"); i >= 0 && strings.TrimSpace(before[i+1:]) == "" {
			return before[i+1:]
		}
	}
	return parent + step
}

// Qwen Code keeps MCP servers under mcpServers in the same settings.json the
// title hooks live in. The file is read as JSONC and patched in place, so
// every other setting keeps its bytes.
var qwenServerPath = []string{"mcpServers", MCPServerName}

type qwenMCP struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// QwenRecallStatus reads mcpServers.another from Qwen Code's settings.json.
func QwenRecallStatus(configDir string) Status {
	file := QwenSettingsFile(configDir)
	status := Status{ConfigDir: configDir, Dir: file, State: StateMissing}
	data, err := os.ReadFile(file)
	if err != nil {
		return status
	}
	std, err := hujson.Standardize(data)
	if err != nil {
		status.State = StateForeign
		return status
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(std, &doc); err != nil {
		status.State = StateForeign
		return status
	}
	raw, ok := doc.MCPServers[MCPServerName]
	if !ok {
		return status
	}
	var entry qwenMCP
	if err := json.Unmarshal(raw, &entry); err != nil || entry.Command == "" {
		status.State = StateForeign
		return status
	}
	status.State = recallState(append([]string{entry.Command}, entry.Args...))
	return status
}

// InstallQwenRecall adds mcpServers.another to Qwen Code's settings.json.
func InstallQwenRecall(status Status, _, _ string, force bool) (Status, error) {
	if status.State == StateCurrent {
		return status, nil
	}
	if status.State.Blocked() && !force {
		return status, fmt.Errorf("%s already has an MCP server named %q that another did not add; remove it or rerun with --force", util.TildePath(status.Dir), MCPServerName)
	}
	command := MCPCommand()
	err := patchJSONC(status.Dir, func(root *hujson.Object, nl string) error {
		return setMember(root, nl, qwenServerPath, qwenMCP{Command: command[0], Args: command[1:]})
	})
	if err != nil {
		return status, err
	}
	return QwenRecallStatus(status.ConfigDir), nil
}

// RemoveQwenRecall takes back mcpServers.another and nothing else.
func RemoveQwenRecall(status Status, force bool) error {
	if !status.State.Installed() {
		return nil
	}
	if status.State.Blocked() && !force {
		return fmt.Errorf("%s holds an MCP server named %q that another did not add; remove it by hand or rerun with --force", util.TildePath(status.Dir), MCPServerName)
	}
	return patchJSONC(status.Dir, func(root *hujson.Object, _ string) error {
		removeMember(root, qwenServerPath)
		return nil
	})
}
