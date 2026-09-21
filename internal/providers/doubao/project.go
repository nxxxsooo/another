package doubao

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxTrajectoryLine = 16 << 20

// projectPath returns the native per-chat workspace when a structured tool
// argument names a file below it. Doubao's cloud conversation does not carry a
// cwd, so a session without that evidence belongs to the native chats root.
func (p *Provider) projectPath(id string) (string, error) {
	root, err := filepath.Abs(p.chats)
	if err != nil {
		return "", err
	}
	pattern := filepath.Join(p.sessionsRoot(), id, "agents", "*", "system", "trajectory.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	var found string
	for _, path := range files {
		candidate, err := trajectoryProject(path, root)
		if err != nil {
			return "", err
		}
		if candidate == "" {
			continue
		}
		if found != "" && found != candidate {
			return root, nil
		}
		found = candidate
	}
	if found == "" {
		return root, nil
	}
	return found, nil
}

func trajectoryProject(path, chatsRoot string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxTrajectoryLine)
	var found string
	for scanner.Scan() {
		var row struct {
			ToolCalls []struct {
				Function struct {
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		for _, call := range row.ToolCalls {
			var args map[string]any
			if json.Unmarshal(call.Function.Arguments, &args) != nil {
				var encoded string
				if json.Unmarshal(call.Function.Arguments, &encoded) != nil || json.Unmarshal([]byte(encoded), &args) != nil {
					continue
				}
			}
			for _, key := range []string{"file_path", "path"} {
				value, _ := args[key].(string)
				candidate := chatWorkspace(chatsRoot, value)
				if candidate == "" {
					continue
				}
				if found != "" && found != candidate {
					return "", nil
				}
				found = candidate
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return found, nil
}

func chatWorkspace(chatsRoot, value string) string {
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	clean := filepath.Clean(value)
	rel, err := filepath.Rel(chatsRoot, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 3 || !validChatDate(parts[0]) || !validChatDir(parts[1]) {
		return ""
	}
	workspace := filepath.Join(chatsRoot, parts[0], parts[1])
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return ""
	}
	return workspace
}

func validChatDate(value string) bool {
	if len(value) != len("2006-01-02") || value[4] != '-' || value[7] != '-' {
		return false
	}
	for i, c := range value {
		if i == 4 || i == 7 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func validChatDir(value string) bool {
	if value == "new-chat" {
		return true
	}
	if !strings.HasPrefix(value, "new-chat-") {
		return false
	}
	suffix := strings.TrimPrefix(value, "new-chat-")
	if suffix == "" {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
