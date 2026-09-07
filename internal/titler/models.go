package titler

import (
	"bytes"
	"context"
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

// modelLister describes how one agent CLI names its own models. Only CLIs with
// a real listing command appear here; the rest fall back to typing a name,
// because guessing model IDs for them would produce a picker full of values
// their --model flag rejects.
type modelLister struct {
	args  []string
	parse func(string) []string
}

var modelListers = map[string]modelLister{
	// pi prints a padded table whose first column is the provider; its
	// --model flag takes "provider/id", so the two are joined back up.
	"pi": {[]string{"--list-models"}, parseTableModels},
	// agy prints "id\tDisplay Name" after a progress line.
	"agy": {[]string{"models"}, parseTabbedModels},
	// Both OpenCode generations print one "provider/model" per line.
	"opencode":  {[]string{"models"}, parsePlainModels},
	"opencode2": {[]string{"models"}, parsePlainModels},
}

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
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, ListTimeout)
	defer cancel()

	// Some CLIs spend their first run in a new directory initializing state
	// and exit successfully with no output. Measured on OpenCode 2: run one
	// prints nothing, run two prints the catalog. The second attempt reuses
	// the same directory, which is exactly what makes it warm.
	var models []string
	for attempt := 0; attempt < 2 && len(models) == 0; attempt++ {
		cmd := exec.CommandContext(ctx, bin, lister.args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "NO_COLOR=1", "CLICOLOR=0", "TERM=dumb")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil, &ListError{Reason: ListTimedOut, Command: l.command}
			}
			return nil, &ListError{Reason: ListFailed, Command: l.command, Detail: failureReason(stderr.String(), stdout.String(), err)}
		}
		models = dedupe(lister.parse(stdout.String()))
	}
	if len(models) == 0 {
		return nil, &ListError{Reason: ListEmpty, Command: l.command}
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
