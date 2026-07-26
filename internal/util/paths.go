package util

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NormalizeProjectPath cleans a project path and resolves symlinks when possible.
func NormalizeProjectPath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// TildePath replaces the user home directory prefix with ~.
func TildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return strings.Replace(p, home, "~", 1)
}

// HomeDir returns the normalized user home directory, or "" if unknown.
func HomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return NormalizeProjectPath(home)
}

// ProjectPathMatchesCWD reports whether a stored project path belongs to the cwd filter.
func ProjectPathMatchesCWD(projectPath, cwd string) bool {
	projectPath = NormalizeProjectPath(projectPath)
	cwd = NormalizeProjectPath(cwd)
	if projectPath == "" || cwd == "" {
		return false
	}
	home := HomeDir()
	if home != "" && cwd == home {
		return strings.HasPrefix(projectPath, home+string(filepath.Separator))
	}
	return projectPath == cwd
}

func EncodeClaudeProjectPath(absPath string) string {
	abs, err := filepath.Abs(absPath)
	if err != nil {
		abs = absPath
	}
	return strings.ReplaceAll(abs, string(filepath.Separator), "-")
}

func DecodeClaudeProjectPath(encoded string) string {
	if encoded == "" {
		return ""
	}
	if strings.HasPrefix(encoded, "-") {
		return "/" + strings.ReplaceAll(encoded[1:], "-", "/")
	}
	return strings.ReplaceAll(encoded, "-", string(filepath.Separator))
}

// DecodeCursorProjectPath decodes ~/.cursor/projects/home-cyrus-... directory names.
func DecodeCursorProjectPath(encoded string) string {
	if encoded == "" {
		return ""
	}
	// heal dirs written by an old agenthop bug that doubled the prefix
	if strings.HasPrefix(encoded, "home-home-") {
		encoded = encoded[len("home-"):]
	}
	if strings.HasPrefix(encoded, "home-") {
		return "/" + strings.ReplaceAll(encoded, "-", string(filepath.Separator))
	}
	return DecodeClaudeProjectPath(encoded)
}

func FileMtime(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

func ReadJSONLLines(path string, maxLines int, fn func(line []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	n := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
		n++
		if maxLines > 0 && n >= maxLines {
			break
		}
	}
	return sc.Err()
}

// ScanJSONLEdges checks the first headLines and trailing tailChunk bytes for a matching line.
func ScanJSONLEdges(path string, headLines int, tailChunk int64, match func(line []byte) bool) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	// ponytail: cap head scan at 512KB — migration metadata lives in the first
	// few lines or the tail; unbounded head reads made dedup walks scan GBs.
	sc := bufio.NewScanner(io.LimitReader(f, 512*1024))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for i := 0; i < headLines && sc.Scan(); i++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if match(line) {
			return true
		}
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	start := int64(0)
	if st.Size() > tailChunk {
		start = st.Size() - tailChunk
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return false
	}
	tailSc := bufio.NewScanner(f)
	tailSc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for tailSc.Scan() {
		line := bytes.TrimSpace(tailSc.Bytes())
		if len(line) == 0 {
			continue
		}
		if match(line) {
			return true
		}
	}
	return false
}

func TailJSONLLines(path string, maxLines int) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// ponytail: fixed 256KB tail window instead of reading the whole file;
	// a final line longer than the window is dropped and callers fall back.
	const tailChunk = 256 * 1024
	truncated := st.Size() > tailChunk
	if truncated {
		if _, err := f.Seek(st.Size()-tailChunk, io.SeekStart); err != nil {
			return nil, err
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if truncated {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	lines := bytes.Split(data, []byte("\n"))
	var out [][]byte
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		out = append([][]byte{line}, out...)
	}
	return out, nil
}

func ParseTime(s string) time.Time {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func JSONUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func JSONMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func FirstUserSnippet(text string, max int) string {
	return TruncateRunes(strings.TrimSpace(text), max)
}

func MatchID(id, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	id = strings.ToLower(id)
	if query == "" {
		return true
	}
	if id == query {
		return true
	}
	if strings.HasSuffix(id, query) {
		return true
	}
	if strings.HasPrefix(id, query) {
		return true
	}
	return false
}
