package titler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ListTimeout bounds one model listing. Some CLIs answer from a local catalog
// and some call their provider, so this is longer than a keystroke but short
// enough that setup never looks hung.
const ListTimeout = 30 * time.Second

// maxModels caps a listing. A catalog this long is already unusable in a
// picker, and an unbounded list would let a misbehaving CLI fill the screen.
const maxModels = 400

// modelLister describes how one agent CLI names its own models. Only CLIs that
// can really answer the question appear here; the rest fall back to typing a
// name, because guessing model IDs for them would produce a picker full of
// values their --model flag rejects.
type modelLister struct {
	args []string
	// stdin is written to the CLI for the agents that take the question on
	// their wire protocol instead of as a subcommand. Empty means the
	// command needs no input, and the CLI reads from the null device.
	stdin string
	parse func(string) []string
	// next is asked when this way of asking names no model. An agent gets a
	// second entry only when the first one can be unavailable for a reason
	// that is not the user's answer — an older build without the command,
	// or a server that is not running.
	next *modelLister
}

var modelListers = map[string]modelLister{
	// pi prints a padded table whose first column is the provider; its
	// --model flag takes "provider/id", so the two are joined back up.
	"pi": {args: []string{"--list-models"}, parse: parseTableModels},
	// agy prints "id\tDisplay Name" after a progress line.
	"agy": {args: []string{"models"}, parse: parseTabbedModels},
	// Both OpenCode generations answer from their own server, which is the
	// catalog /models shows inside the client.
	"opencode":  {args: []string{"api", "GET", openCodeModelPath}, parse: parseOpenCodeAPIModels, next: &openCodeSubcommand},
	"opencode2": {args: []string{"api", "GET", openCodeModelPath}, parse: parseOpenCodeAPIModels, next: &openCodeSubcommand},
	// Qwen Code exposes its current auth type's catalog through the headless
	// control protocol. The first request enables that protocol; the second
	// returns the exact IDs accepted by --model. Do not use --bare here: it
	// would discard the auth/provider settings that determine the catalog.
	"qwen": {
		args:  []string{"--safe-mode", "--chat-recording=false", "--input-format", "stream-json", "--output-format", "stream-json"},
		stdin: qwenListRequest,
		parse: parseQwenModels,
	},
	// Claude Code has no listing subcommand, but its headless control
	// protocol answers list_models from the same catalog its own /model
	// picker shows: no prompt, no model call, and it exits as soon as stdin
	// closes. --bare keeps the question out of the user's hooks, plugins,
	// and CLAUDE.md, which a listing has no business loading.
	"claude-code": {
		args:  []string{"-p", "--bare", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"},
		stdin: claudeListRequest,
		parse: parseClaudeModels,
	},
}

// openCodeModelPath is the endpoint behind the client's own /models list: the
// resolved catalog, custom providers from the user's config included.
const openCodeModelPath = "/api/model"

// openCodeSubcommand is the fallback for an OpenCode too old to expose that
// endpoint. It prints the same identifiers, but it resolves them for the
// directory it was started in, so a directory the client has never opened
// answers with a catalog that is still settling: nothing at all, then the
// whole of models.dev, and only later the user's own providers. That is why
// the server is asked first.
var openCodeSubcommand = modelLister{args: []string{"models"}, parse: parsePlainModels}

// claudeListRequest is the one control request Claude Code needs to answer
// with its catalog. The id is only echoed back, so it names another rather
// than pretending to be a counter.
const claudeListRequest = `{"type":"control_request","request_id":"another-list-models","request":{"subtype":"list_models"}}` + "\n"

const qwenListRequest = `{"type":"control_request","request_id":"another-init","request":{"subtype":"initialize"}}` + "\n" +
	`{"type":"control_request","request_id":"another-list-models","request":{"subtype":"get_available_models"}}` + "\n"

// ListFailure names why a listing did not produce models. Like a freeze
// reason, it is an identifier rather than a sentence: the screen that shows it
// decides the words, and this package has no business holding one language's.
type ListFailure string

const (
	// ListUnsupported means the agent has no listing command at all.
	ListUnsupported ListFailure = "unsupported"
	// ListNoTitles means the agent cannot write titles in the first place.
	ListNoTitles ListFailure = "no-titles"
	// ListNotInstalled means the CLI is not on PATH.
	ListNotInstalled ListFailure = "not-installed"
	// ListTimedOut means the CLI did not answer within ListTimeout.
	ListTimedOut ListFailure = "timed-out"
	// ListEmpty means the CLI answered without naming a model.
	ListEmpty ListFailure = "empty"
	// ListFailed means the CLI exited with an error of its own, which
	// Detail carries verbatim because only the CLI can explain it.
	ListFailed ListFailure = "failed"
)

// ListError is what ListModels returns. Command is the CLI that was asked, so
// a caller can name it in its own wording; Detail is text that cannot be
// translated because it came from the CLI itself.
type ListError struct {
	Reason  ListFailure
	Command string
	Detail  string
}

// Error is the untranslated form, used when no interface is present to render
// a better one — a log line, or a caller that only has an error.
func (e *ListError) Error() string {
	switch e.Reason {
	case ListUnsupported:
		return fmt.Sprintf("%s cannot list its models", e.Command)
	case ListNoTitles:
		return fmt.Sprintf("%s cannot write titles", e.Command)
	case ListNotInstalled:
		return fmt.Sprintf("%s is not installed", e.Command)
	case ListTimedOut:
		return fmt.Sprintf("%s timed out listing models", e.Command)
	case ListEmpty:
		return fmt.Sprintf("%s returned no models", e.Command)
	default:
		return fmt.Sprintf("%s: %s", e.Command, e.Detail)
	}
}

// CanListModels reports whether setup can offer a picker for this agent.
func CanListModels(providerID string) bool {
	_, ok := modelListers[NormalizeID(providerID)]
	return ok
}

// ListModels asks the agent CLI which models it can run. An error means the
// caller should let the person type a name instead; it is never fatal.
func ListModels(ctx context.Context, providerID string) ([]string, error) {
	id := NormalizeID(providerID)
	lister, ok := modelListers[id]
	if !ok {
		return nil, &ListError{Reason: ListUnsupported, Command: providerID}
	}
	l, ok := launchers[id]
	if !ok {
		return nil, &ListError{Reason: ListNoTitles, Command: providerID}
	}
	bin, err := exec.LookPath(l.command)
	if err != nil {
		return nil, &ListError{Reason: ListNotInstalled, Command: l.command}
	}

	// The same throwaway directory rule as a suggestion: listing models is a
	// read, but some CLIs still load project configuration and write session
	// state from wherever they start.
	dir, err := os.MkdirTemp("", "another-models-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ctx, cancel := context.WithTimeout(ctx, ListTimeout)
	defer cancel()

	// A silent or failed way of asking is not an answer, so the next one is
	// tried before the person is sent off to type a name.
	var failed error
	for attempt := &lister; attempt != nil; attempt = attempt.next {
		models, err := runLister(ctx, *attempt, bin, l.command, dir)
		if len(models) > 0 {
			return models, nil
		}
		if err != nil {
			if listErr, ok := err.(*ListError); ok && listErr.Reason == ListTimedOut {
				return nil, err
			}
			failed = err
		}
	}
	if failed != nil {
		return nil, failed
	}
	return nil, &ListError{Reason: ListEmpty, Command: l.command}
}

// runLister asks one way and reads the answer back.
func runLister(ctx context.Context, lister modelLister, bin, command, dir string) ([]string, error) {
	// Some CLIs spend their first run in a new directory initializing state
	// and exit successfully with no output. Measured on OpenCode 2: run one
	// prints nothing, run two prints the catalog. The second attempt reuses
	// the same directory, which is exactly what makes it warm.
	var models []string
	for attempt := 0; attempt < 2 && len(models) == 0; attempt++ {
		cmd := exec.CommandContext(ctx, bin, lister.args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "NO_COLOR=1", "CLICOLOR=0", "TERM=dumb")
		if lister.stdin != "" {
			cmd.Stdin = strings.NewReader(lister.stdin)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil, &ListError{Reason: ListTimedOut, Command: command}
			}
			return nil, &ListError{Reason: ListFailed, Command: command, Detail: failureReason(stderr.String(), stdout.String(), err)}
		}
		models = dedupe(lister.parse(stdout.String()))
	}
	return models, nil
}

// parseTableModels reads a padded "provider model ..." table and rejoins the
// two columns into the identifier the CLI accepts.
func parseTableModels(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(normalizeLine(line))
		if len(fields) < 2 || fields[0] == "provider" {
			continue
		}
		out = append(out, fields[0]+"/"+fields[1])
	}
	return out
}

// parseTabbedModels keeps the identifier column and drops any progress or
// summary line that carries no tab.
func parseTabbedModels(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = normalizeLine(line)
		id, _, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// parsePlainModels keeps one identifier per line. A line with whitespace in it
// is prose — a heading, a warning, a total — not a model ID.
func parsePlainModels(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = normalizeLine(line)
		if line == "" || strings.ContainsAny(line, " \t") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// parseOpenCodeAPIModels reads the catalog OpenCode's server answers with. The
// identifier its --model flag takes is the provider and the model's own id
// joined by a slash: id, not modelID, because a family like claude-opus-5-fast
// shares one modelID with its siblings. An answer that is not this catalog —
// an HTTP status line, the web client's HTML, an older build's usage text —
// parses to nothing and hands the question to the next way of asking.
func parseOpenCodeAPIModels(raw string) []string {
	var body struct {
		Data []struct {
			ProviderID string `json:"providerID"`
			ID         string `json:"id"`
			Enabled    *bool  `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &body); err != nil {
		return nil
	}
	var out []string
	for _, model := range body.Data {
		provider := strings.TrimSpace(model.ProviderID)
		id := strings.TrimSpace(model.ID)
		if provider == "" || id == "" {
			continue
		}
		if model.Enabled != nil && !*model.Enabled {
			continue
		}
		out = append(out, provider+"/"+id)
	}
	return out
}

// parseClaudeModels reads the control_response Claude Code writes for a
// list_models request and keeps the value its --model flag accepts. Every
// other line on that stream — hook noise, system messages, a response to some
// other request — is ignored, and the catalog's own "default" entry is dropped
// because the picker already offers the CLI's default as its first row.
func parseClaudeModels(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var msg struct {
			Type     string `json:"type"`
			Response struct {
				Subtype  string `json:"subtype"`
				Response struct {
					Models []struct {
						Value string `json:"value"`
					} `json:"models"`
				} `json:"response"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Type != "control_response" || msg.Response.Subtype != "success" {
			continue
		}
		for _, model := range msg.Response.Response.Models {
			if value := strings.TrimSpace(model.Value); value != "" && value != "default" {
				out = append(out, value)
			}
		}
	}
	return out
}

// parseQwenModels reads only the successful response to another's catalog
// request. The initialize response and any stream noise are deliberately
// ignored; model.id is the value Qwen Code's --model flag accepts.
func parseQwenModels(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var msg struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string `json:"subtype"`
				RequestID string `json:"request_id"`
				Response  struct {
					Subtype string `json:"subtype"`
					Models  []struct {
						ID string `json:"id"`
					} `json:"models"`
				} `json:"response"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil ||
			msg.Type != "control_response" || msg.Response.Subtype != "success" ||
			msg.Response.RequestID != "another-list-models" ||
			msg.Response.Response.Subtype != "get_available_models" {
			continue
		}
		for _, model := range msg.Response.Response.Models {
			if id := strings.TrimSpace(model.ID); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

func dedupe(models []string) []string {
	seen := make(map[string]bool, len(models))
	out := make([]string, 0, len(models))
	for _, name := range models {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) >= maxModels {
			break
		}
	}
	return out
}
