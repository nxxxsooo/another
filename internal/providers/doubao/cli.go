package doubao

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
)

func cliAvailable() bool { _, err := exec.LookPath("doubao"); return err == nil }

func cliList(ctx context.Context) (map[string]string, error) {
	b, err := exec.CommandContext(ctx, "doubao", "--list").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// ExitError output may contain sensitive native account details.
		return nil, fmt.Errorf("doubao --list failed; check CLI login")
	}
	return parseCLIList(string(b))
}

// Native cloud IDs stay strings throughout, including IDs above 2^53.
func parseCLIList(output string) (map[string]string, error) {
	result := map[string]string{}
	s := bufio.NewScanner(strings.NewReader(output))
	s.Buffer(make([]byte, 4096), 1<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		i := strings.IndexFunc(line, unicode.IsSpace)
		if i < 0 {
			i = len(line)
		}
		id := line[:i]
		if !validID(id) {
			// The CLI may print headings, but never treat arbitrary text as an ID.
			continue
		}
		title := strings.TrimSpace(line[i:])
		if previous, ok := result[id]; ok && previous != title {
			return nil, fmt.Errorf("doubao --list returned conflicting duplicate identities")
		}
		result[id] = title
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("invalid doubao --list output")
	}
	if len(result) == 0 && strings.TrimSpace(output) != "" {
		return nil, fmt.Errorf("doubao --list returned no recognizable session identities; check CLI login")
	}
	return result, nil
}
