package titler

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OpenCode V2 accepts a caller-supplied ID beginning with "ses". Remembering
// it before starting the process lets us clean up even if it produces no output.
func newOpenCodeSessionID() string {
	var id [16]byte
	_, _ = rand.Read(id[:]) // Go 1.25's rand.Read cannot return an error.
	return fmt.Sprintf("ses_%x", id)
}

func openCodeArgs(cfg Config, sessionID string) []string {
	// Keep the synthetic title one word so cmd.exe preserves the quoting
	// around a launcher path containing spaces.
	args := []string{"run", "--archived", "--title", "another-title-suggestion", "--session", sessionID}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	return args
}

// OpenCode persists one project per directory and caches services by location.
// Keep one location for both CLI names; eviction could interrupt a parallel run.
func openCodeWorkDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	const name = TempDirPrefix + "opencode"
	dir := filepath.Join(cache, "another", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if filepath.Base(dir) != name {
		return "", fmt.Errorf("OpenCode titler directory resolved outside its reserved name: %q", dir)
	}
	return dir, requireEmptyOpenCodeWorkDir(dir)
}

func requireEmptyOpenCodeWorkDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("OpenCode titler directory is not empty: %q", dir)
	}
	return nil
}

func removeOpenCodeSession(bin, dir, sessionID string) error {
	action := "delete OpenCode titler session " + sessionID
	// Cleanup has its own deadline so cancellation of the run cannot skip it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "api", "DELETE", "/api/session/"+sessionID)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", action, ctx.Err())
	}
	// A failed run may never have created the session. Only tolerate the
	// server's typed not-found response for this exact ID, not a missing route.
	for _, line := range strings.Split(string(out), "\n") {
		var response struct {
			Tag       string `json:"_tag"`
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal([]byte(line), &response) == nil && response.Tag == "SessionNotFoundError" && response.SessionID == sessionID {
			return nil
		}
	}
	// This route returns a silent 204. Some CLI versions print an HTTP
	// failure but still exit zero, so a nonempty response is also an error.
	if err == nil {
		if strings.TrimSpace(string(out)) == "" {
			return nil
		}
		err = errors.New("expected an empty OpenCode API response")
	}
	return fmt.Errorf("%s: %s", action, failureReason(string(out), "", err))
}
