package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// renameNative delegates storage ownership to Codex. Editing threads.title or
// session_index.jsonl ourselves can make another display a name the client has
// never accepted. A metadata-only native read verifies the same thread's name.
func (p *Provider) renameNative(ctx context.Context, sessionID, title string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--stdio")
	if p.appServerCommand != nil {
		cmd = p.appServerCommand(ctx)
	}
	cmd.Dir = filepath.Dir(p.sessionsRoot)
	cmd.Env = append(cmd.Environ(), "CODEX_HOME="+filepath.Dir(p.sessionsRoot))
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdout.Close() }()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codex: start native title service (codex app-server): %w", err)
	}
	defer func() {
		_ = stdin.Close()
		// Only this dedicated metadata service is ours. Never stop the user's
		// shared daemon or resume/interrupt the thread being renamed.
		timer := time.AfterFunc(time.Second, cancel)
		_ = cmd.Wait()
		timer.Stop()
	}()
	client := codexRPC{ctx: ctx, writer: json.NewEncoder(stdin), reader: bufio.NewScanner(stdout)}
	client.reader.Buffer(make([]byte, 4096), 1024*1024)
	if _, err := client.request("initialize", map[string]any{
		"clientInfo": map[string]string{"name": "another", "version": "1"},
	}); err != nil {
		return err
	}
	if err := client.writer.Encode(map[string]string{"method": "initialized"}); err != nil {
		return fmt.Errorf("codex: initialize native title service: %w", err)
	}
	if _, err := client.request("thread/name/set", map[string]string{"threadId": sessionID, "name": title}); err != nil {
		return err
	}
	result, err := client.request("thread/read", map[string]any{"threadId": sessionID, "includeTurns": false})
	if err != nil {
		return fmt.Errorf("codex: title update acknowledged but readback failed: %w", err)
	}
	var readback struct {
		Thread struct {
			ID   string  `json:"id"`
			Name *string `json:"name"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &readback); err != nil {
		return fmt.Errorf("codex: decode title readback: %w", err)
	}
	if readback.Thread.ID != sessionID || readback.Thread.Name == nil || *readback.Thread.Name != title {
		return fmt.Errorf("codex: native title readback did not confirm the requested name for session %s", sessionID)
	}
	return nil
}

type codexRPC struct {
	ctx    context.Context
	writer *json.Encoder
	reader *bufio.Scanner
	nextID int
}

func (c *codexRPC) request(method string, params any) (json.RawMessage, error) {
	c.nextID++
	if err := c.writer.Encode(map[string]any{"id": c.nextID, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("codex %s: send request: %w", method, err)
	}
	for c.reader.Scan() {
		var response struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(c.reader.Bytes(), &response); err != nil {
			return nil, fmt.Errorf("codex %s: invalid native response: %w", method, err)
		}
		if len(response.ID) == 0 {
			continue // Asynchronous notifications may precede a response.
		}
		if response.Method != "" {
			return nil, fmt.Errorf("codex %s: native service requires interaction (%s)", method, response.Method)
		}
		if string(response.ID) != strconv.Itoa(c.nextID) {
			return nil, fmt.Errorf("codex %s: unexpected response id", method)
		}
		if response.Error != nil {
			return nil, fmt.Errorf("codex %s: %s (code %d)", method, response.Error.Message, response.Error.Code)
		}
		if len(response.Result) == 0 {
			return nil, fmt.Errorf("codex %s: native response has no result", method)
		}
		return response.Result, nil
	}
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.reader.Err(); err != nil {
		return nil, fmt.Errorf("codex %s: read response: %w", method, err)
	}
	return nil, fmt.Errorf("codex %s: native service closed before responding: %w", method, io.ErrUnexpectedEOF)
}
