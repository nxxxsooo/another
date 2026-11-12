// Package herdr is an optional, local-only bridge to an already running Herdr.
// It never indexes terminals as saved sessions or infers identity from paths.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes a program with literal arguments, without an intervening shell.
type Runner func(context.Context, string, ...string) ([]byte, error)

type Client struct{ Run Runner }

const maxErrorStderr = 1024

func command(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 200 * time.Millisecond
	return cmd.Output()
}

func (c Client) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	run := c.Run
	if run == nil {
		run = command
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := run(ctx, name, args...)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			detail := strings.TrimSpace(string(exit.Stderr))
			if len(detail) > maxErrorStderr {
				detail = detail[:maxErrorStderr] + " [truncated]"
			}
			if detail != "" {
				err = fmt.Errorf("%w: %s", err, detail)
			}
		}
		return nil, fmt.Errorf("herdr: %s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

func (c Client) read(ctx context.Context, name string, dst any, args ...string) error {
	out, err := c.run(ctx, name, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, dst); err != nil {
		return fmt.Errorf("herdr: invalid %s response: %w", name, err)
	}
	return nil
}

type terminal struct {
	Address string          `json:"address"`
	Agent   string          `json:"agent"`
	State   string          `json:"state"`
	Machine json.RawMessage `json:"machine"`
}

func local(t terminal) bool {
	return t.Address != "" && !strings.ContainsAny(t.Address, "/\\") &&
		(len(t.Machine) == 0 || bytes.Equal(t.Machine, []byte("null")))
}

// Snapshot maps exact native OpenCode IDs to live local terminal addresses.
// Positive identities survive unrelated probe failures. A non-nil error means
// negative knowledge is incomplete: callers must not launch a new session.
func (c Client) Snapshot(ctx context.Context) (map[string][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	// Unlike ls/get, server status does not auto-start an absent core.
	var status struct {
		PID int `json:"pid"`
	}
	if err := c.read(ctx, "hc", &status, "server", "status", "--json"); err != nil {
		return nil, err
	}
	if status.PID <= 0 {
		return nil, fmt.Errorf("herdr: local core is not running")
	}
	var listing struct {
		Terminals *[]terminal `json:"terminals"`
	}
	if err := c.read(ctx, "hc", &listing, "ls", "--json"); err != nil {
		return nil, err
	}
	if listing.Terminals == nil {
		return nil, fmt.Errorf("herdr: terminal list is missing")
	}
	live := make(map[string][]string)
	seen := make(map[string]bool)
	var problems []error
	incomplete := func(address string, err error) {
		problems = append(problems, fmt.Errorf("herdr: terminal %s native metadata incomplete: %w; inspect hc get %s --json and restore native session reporting before opening a saved session", address, err, address))
	}
	for _, t := range *listing.Terminals {
		if t.Agent == "opencode" && t.Address == "" {
			problems = append(problems, fmt.Errorf("herdr: terminal address is missing; inspect hc ls --json and restore native session reporting before opening a saved session"))
			continue
		}
		if !local(t) || t.Agent != "opencode" || seen[t.Address] {
			continue
		}
		if t.State == "finished" {
			continue
		}
		if t.State != "live" {
			incomplete(t.Address, fmt.Errorf("unknown terminal state %q", t.State))
			continue
		}
		seen[t.Address] = true
		var detail struct {
			Address string          `json:"address"`
			State   string          `json:"state"`
			Status  json.RawMessage `json:"status"`
			Records map[string]struct {
				App   string `json:"app"`
				State string `json:"state"`
			} `json:"records"`
		}
		if err := c.read(ctx, "hc", &detail, "get", t.Address, "--json"); err != nil {
			incomplete(t.Address, err)
			continue
		}
		if detail.Address != t.Address || detail.State == "" {
			incomplete(t.Address, fmt.Errorf("invalid terminal detail"))
			continue
		}
		// Finished terminals and shells can retain historical records.
		if detail.State != "live" && detail.State != "finished" {
			incomplete(t.Address, fmt.Errorf("unknown terminal state %q", detail.State))
			continue
		}
		if detail.State == "finished" {
			continue
		}
		if len(detail.Status) == 0 {
			incomplete(t.Address, fmt.Errorf("terminal status unavailable"))
			continue
		}
		if bytes.Equal(detail.Status, []byte("null")) {
			continue
		}
		var status struct {
			App string `json:"app"`
		}
		if err := json.Unmarshal(detail.Status, &status); err != nil || status.App == "" {
			incomplete(t.Address, fmt.Errorf("invalid terminal status"))
			continue
		}
		if status.App != "opencode" {
			continue
		}
		if detail.Records == nil {
			incomplete(t.Address, fmt.Errorf("native session records unavailable"))
			continue
		}
		for id, record := range detail.Records {
			if record.App == "" || id == "" {
				incomplete(t.Address, fmt.Errorf("invalid native session record"))
				continue
			}
			if record.App != "opencode" {
				continue
			}
			switch record.State {
			case "idle", "working", "done", "blocked", "error":
				live[id] = append(live[id], t.Address)
			case "finished", "exited": // retained, no longer live
			default:
				incomplete(t.Address, fmt.Errorf("unknown session state %q", record.State))
			}
		}
	}
	return live, errors.Join(problems...)
}

func Supported(provider string) bool { return provider == "opencode" }

type Request struct {
	Provider, ID, Name, Folder string
	// Resume is evaluated only after absence has been positively established.
	// It returns the provider's native launch argv, preserving its own quoting.
	Resume func() ([]string, error)
}

// Open focuses an exact live terminal, or opens the saved session in a new tab.
func (c Client) Open(ctx context.Context, req Request) (string, error) {
	if !Supported(req.Provider) || req.ID == "" {
		return "", fmt.Errorf("herdr: only local OpenCode sessions are supported")
	}
	live, err := c.Snapshot(ctx)
	matches := live[req.ID]
	if len(matches) > 1 {
		return "", fmt.Errorf("herdr: ambiguous native session %s in terminals %s; no action taken", req.ID, strings.Join(matches, ", "))
	}
	if len(matches) == 1 {
		if err := c.do(ctx, "agent.focus", map[string]any{"terminal": matches[0]}); err != nil {
			return "", err // Never fall back to a duplicate after a failed focus.
		}
		return "Focused Herdr terminal " + matches[0] + "; you may need to switch its internal OpenCode tab", nil
	}
	if err != nil {
		return "", err
	}
	if req.Resume == nil || req.Folder == "" {
		return "", fmt.Errorf("herdr: native resume command and local project folder are required")
	}
	argv, err := req.Resume()
	if err != nil {
		return "", err
	}
	if len(argv) == 0 || argv[0] == "" {
		return "", fmt.Errorf("herdr: native resume command is empty")
	}
	var layout struct {
		Spaces *[]struct {
			ID      string          `json:"id"`
			Machine json.RawMessage `json:"machine"`
			Active  bool            `json:"active"`
		} `json:"spaces"`
	}
	if err := c.read(ctx, "herdr-app", &layout, "ls"); err != nil {
		return "", err
	}
	space := ""
	if layout.Spaces != nil {
		for _, s := range *layout.Spaces {
			if s.ID != "" && bytes.Equal(s.Machine, []byte("null")) {
				if space == "" || s.Active {
					space = s.ID
				}
				if s.Active {
					break
				}
			}
		}
	}
	if space == "" {
		return "", fmt.Errorf("herdr: no verified local space; open a local Herdr space first")
	}
	name := req.Name
	if strings.TrimSpace(name) == "" {
		name = req.ID
	}
	if err := c.do(ctx, "tab.new", map[string]any{"space": space, "name": name, "folder": req.Folder, "argv": argv}); err != nil {
		return "", err
	}
	return "Opened native OpenCode session in a new local Herdr tab", nil
}

func (c Client) do(ctx context.Context, action string, args map[string]any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	var reply map[string]json.RawMessage
	if err := c.read(ctx, "herdr-app", &reply, "do", action, string(data)); err != nil {
		return err
	}
	if reply == nil {
		return fmt.Errorf("herdr: empty command response")
	}
	if failure, ok := reply["error"]; ok {
		return fmt.Errorf("herdr: %s", failure)
	}
	return nil
}
