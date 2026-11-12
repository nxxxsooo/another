package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"
)

const watchPrefix = "another.watch."

type LiveSession struct {
	State    string `json:"state"`
	Terminal string `json:"terminal"`
	Title    string `json:"title"`
}

type WatchStatus struct {
	Live   map[string]LiveSession `json:"live"`
	Groups map[string][]string    `json:"groups"`
}

// Status requires complete discovery: labels confer cross-project read access.
// Reuse Snapshot's native identity validation rather than inventing a second
// interpretation of retained records, remote terminals, or shell status.
func (c Client) Status(ctx context.Context) (WatchStatus, error) {
	result := WatchStatus{Live: map[string]LiveSession{}, Groups: map[string][]string{}}
	type detail struct {
		Labels  map[string]string `json:"labels"`
		Records map[string]struct {
			State string `json:"state"`
			Title string `json:"title"`
		} `json:"records"`
	}
	details := map[string]detail{}
	run := c.Run
	if run == nil {
		run = command
	}
	probe := Client{Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		out, err := run(ctx, name, args...)
		if err == nil && name == "hc" && len(args) == 3 && args[0] == "get" {
			var d detail
			if err := json.Unmarshal(out, &d); err != nil {
				return nil, err
			}
			details[args[1]] = d
		}
		return out, err
	}}
	live, err := probe.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	for id, terminals := range live {
		if len(terminals) != 1 {
			return result, fmt.Errorf("herdr: ambiguous native session %s in terminals %s; no action taken", id, strings.Join(terminals, ", "))
		}
		t := terminals[0]
		r := details[t].Records[id]
		result.Live[id] = LiveSession{State: r.State, Terminal: t, Title: r.Title}
	}
	for supervisor, session := range result.Live {
		value := details[session.Terminal].Labels[watchPrefix+supervisor]
		if value == "" {
			continue
		}
		members := strings.Split(value, ",")
		for _, member := range members {
			if !validWatchID(member) || member == supervisor {
				return result, fmt.Errorf("herdr: invalid watch label for %s", supervisor)
			}
		}
		sort.Strings(members)
		result.Groups[supervisor] = slices.Compact(members)
	}
	return result, nil
}

func validWatchID(id string) bool {
	return strings.HasPrefix(id, "ses_") && len(id) > 4 && !strings.ContainsAny(id, ",= \t\r\n/\\")
}

func (c Client) Groups(ctx context.Context) (map[string][]string, error) {
	status, err := c.Status(ctx)
	return status.Groups, err
}

func (c Client) Watch(ctx context.Context, supervisor, member string) error {
	return c.changeWatch(ctx, supervisor, member, true)
}
func (c Client) Unwatch(ctx context.Context, supervisor, member string) error {
	return c.changeWatch(ctx, supervisor, member, false)
}

func (c Client) changeWatch(ctx context.Context, supervisor, member string, add bool) error {
	if !validWatchID(supervisor) || !validWatchID(member) || supervisor == member {
		return fmt.Errorf("herdr: watch requires two different native OpenCode session IDs")
	}
	s, err := c.Status(ctx)
	if err != nil {
		return err
	}
	for _, id := range []string{supervisor, member} {
		if _, ok := s.Live[id]; !ok {
			return fmt.Errorf("herdr: session %s is not a live local OpenCode session", id)
		}
	}
	members := s.Groups[supervisor]
	if add {
		members = append(members, member)
	} else {
		members = slices.DeleteFunc(members, func(id string) bool { return id == member })
	}
	sort.Strings(members)
	members = slices.Compact(members)
	_, err = c.run(ctx, "hc", "label", s.Live[supervisor].Terminal, watchPrefix+supervisor+"="+strings.Join(members, ","))
	return err
}

type TurnEnd struct {
	Member string    `json:"member"`
	Title  string    `json:"title"`
	State  string    `json:"state"`
	At     time.Time `json:"at"`
}

// Follow consumes the hc event stream. All events refresh membership as labels
// can change independently of a member's record. New members establish a
// baseline; joining a group is not itself a turn completion.
func (c Client) Follow(ctx context.Context, supervisor string, events io.Reader, emit func(TurnEnd) error) error {
	previous := map[string]LiveSession{}
	scan := bufio.NewScanner(events)
	scan.Buffer(make([]byte, 4096), 4<<20)
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event struct {
			Terminals json.RawMessage `json:"terminals"`
			Method    string          `json:"method"`
			Params    struct {
				Terminal string `json:"terminal"`
				Type     string `json:"type"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			return fmt.Errorf("herdr: invalid event: %w", err)
		}
		if len(event.Terminals) == 0 && event.Method != "event" {
			return fmt.Errorf("herdr: invalid event envelope")
		}
		s, err := c.Status(ctx)
		if err != nil {
			return err
		}
		members, exists := s.Groups[supervisor]
		if !exists {
			return nil
		}
		next := map[string]LiveSession{}
		for _, id := range members {
			current, live := s.Live[id]
			if !live {
				continue
			}
			next[id] = current
			old, known := previous[id]
			ended := known && ((old.State == "working" && (current.State == "idle" || current.State == "done")) || (old.State != current.State && (current.State == "blocked" || current.State == "error")))
			if ended {
				if err := emit(TurnEnd{Member: id, Title: current.Title, State: current.State, At: time.Now().UTC()}); err != nil {
					return err
				}
			}
		}
		previous = next
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return io.ErrUnexpectedEOF
}

// FollowCommand owns the streaming child separately from the bounded Runner.
func (c Client) FollowCommand(ctx context.Context, supervisor string, emit func(TurnEnd) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "hc", "events", "--json")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("herdr: hc events: %w", err)
	}
	err = c.Follow(ctx, supervisor, pipe, emit)
	cancel()
	_ = pipe.Close()
	_ = cmd.Wait()
	return err
}
