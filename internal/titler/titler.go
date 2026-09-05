package titler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Timeout bounds one suggestion. A cold agent CLI plus a model round trip is
// slow; past this the suggestion is no longer worth waiting for and the person
// is already typing a title by hand.
const Timeout = 60 * time.Second

// Config selects which installed agent CLI generates the title. An empty
// Provider means the feature is off. An empty Model uses that CLI's default.
type Config struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
}

// Enabled reports whether a suggestion should be attempted at all.
func (c Config) Enabled() bool { return NormalizeID(c.Provider) != "" }

type launcher struct {
	command string
	args    func(cfg Config, prompt string) []string
}

// launchers holds the headless invocation for each agent that can run a
// one-shot prompt. Agents missing here simply cannot generate titles; they are
// left out of the setup list rather than failing at use time.
var launchers = map[string]launcher{
	"claude-code": {"claude", func(c Config, p string) []string {
		args := []string{"-p", "--output-format", "text"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		return append(args, p)
	}},
	"codex": {"codex", func(c Config, p string) []string {
		args := []string{"exec", "--skip-git-repo-check", "--color", "never", "--sandbox", "read-only"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		return append(args, p)
	}},
	"pi": {"pi", func(c Config, p string) []string {
		args := []string{"--print", "--no-session"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		return append(args, "--", p)
	}},
	"opencode": {"opencode", func(c Config, p string) []string {
		args := []string{"run"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		return append(args, p)
	}},
	"opencode2": {"opencode2", func(c Config, p string) []string {
		args := []string{"run"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		return append(args, p)
	}},
	"qwen": {"qwen", func(c Config, p string) []string {
		args := []string{"--bare", "--safe-mode", "--chat-recording=false", "--output-format", "text"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		return append(args, p)
	}},
	// agy is the one CLI here that will not take the prompt as a trailing
	// argument: --print consumes the next token as its value, so a separate
	// prompt argument is silently ignored. It must be attached to the flag.
	// --disable-slash-commands stops the untrusted session content from
	// expanding a slash command or a skill; the contract is inlined in the
	// prompt anyway, so nothing is lost by not loading one.
	"agy": {"agy", func(c Config, p string) []string {
		args := []string{"--output-format", "text", "--disable-slash-commands"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		return append(args, "--print="+p)
	}},
}

// NormalizeID accepts the ids another stores and the aliases people type.
func NormalizeID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.ReplaceAll(id, "_", "-")
	switch id {
	case "claude", "claudecode":
		return "claude-code"
	}
	return id
}

// Supports reports whether this provider can generate titles at all.
func Supports(providerID string) bool {
	_, ok := launchers[NormalizeID(providerID)]
	return ok
}

// Available reports whether the provider can generate titles right now, which
// additionally requires its CLI on PATH.
func Available(providerID string) bool {
	l, ok := launchers[NormalizeID(providerID)]
	if !ok {
		return false
	}
	_, err := exec.LookPath(l.command)
	return err == nil
}

// Command reports the binary a provider would run, for display in setup.
func Command(providerID string) string {
	if l, ok := launchers[NormalizeID(providerID)]; ok {
		return l.command
	}
	return ""
}

// Suggest runs the configured agent once and returns a contract-valid title,
// or "" when the model declined or drifted. An empty result is not an error:
// the caller shows nothing and the manual rename path is untouched.
func Suggest(ctx context.Context, cfg Config, req Request) (string, error) {
	// Missing creation times arrive in two shapes: a zero time from callers
	// that never had one, and Unix 0 from the index, which scans back as 1970
	// rather than a zero time. Either way the date cannot be proven, so this
	// check runs before anything that could spend a model call, and before the
	// CLI lookup so the refusal never depends on what is installed.
	if req.CreatedAt.Unix() <= 0 {
		return "", errors.New("会话缺少创建时间")
	}
	l, ok := launchers[NormalizeID(cfg.Provider)]
	if !ok {
		return "", fmt.Errorf("%s 不能生成标题", cfg.Provider)
	}
	bin, err := exec.LookPath(l.command)
	if err != nil {
		return "", fmt.Errorf("%s 未安装", l.command)
	}

	// A throwaway working directory keeps the agent out of the user's project:
	// no project-level instructions are loaded from untrusted checkouts, and
	// whatever session the CLI records lands outside their real projects.
	dir, err := os.MkdirTemp("", "another-titler-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, l.args(cfg, BuildPrompt(req))...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "CLICOLOR=0", "TERM=dumb")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s 生成超时", l.command)
		}
		return "", fmt.Errorf("%s: %s", l.command, failureReason(stderr.String(), stdout.String(), err))
	}
	return Clean(stdout.String()), nil
}

// failureReason picks the one line worth showing in a status bar.
func failureReason(stderr, stdout string, err error) string {
	for _, source := range []string{stderr, stdout} {
		for _, line := range strings.Split(source, "\n") {
			if line = normalizeLine(line); line != "" {
				return truncateRunes(line, 80)
			}
		}
	}
	return err.Error()
}
