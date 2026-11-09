package doubao

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Desktop trajectories use the native cloud ID. CLI .sessions directories do
// not, and must never be correlated by their title or numeric directory name.
func (p *Provider) projectPath(profile, id string) (string, error) {
	root, err := filepath.Abs(p.chats)
	if err != nil {
		return "", err
	}
	files, err := filepath.Glob(filepath.Join(sessionsRoot(profile), id, "agents", "*", "system", "trajectory.jsonl"))
	if err != nil {
		return "", err
	}
	var found string
	for _, file := range files {
		candidate, conflict, err := trajectoryProject(file, root)
		if err != nil {
			return "", err
		}
		if conflict {
			return root, nil
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

func trajectoryProject(path, chatsRoot string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16<<20)
	var found string
	for s.Scan() {
		var row struct {
			ToolCalls []struct {
				Function struct {
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal(s.Bytes(), &row) != nil {
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
					return "", true, nil
				}
				found = candidate
			}
		}
	}
	return found, false, s.Err()
}

func chatWorkspace(chatsRoot, value string) string {
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	root, err := filepath.Abs(chatsRoot)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, filepath.Clean(value))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 3 {
		return ""
	}
	if _, err := time.Parse("2006-01-02", parts[0]); err != nil {
		return ""
	}
	if !validChatDir(parts[1]) {
		return ""
	}
	workspace := filepath.Join(root, parts[0], parts[1])
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return ""
	}
	return workspace
}

func validChatDir(value string) bool {
	if value == "new-chat" {
		return true
	}
	return strings.HasPrefix(value, "new-chat-") && validID(strings.TrimPrefix(value, "new-chat-"))
}
