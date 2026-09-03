package util_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

// TailJSONLLines must return the last lines of a file larger than its internal
// tail window without reading the whole file.
func TestTailJSONLLinesLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.jsonl")
	pad := strings.Repeat("x", 1024)
	var b strings.Builder
	for i := 0; i < 1000; i++ { // ~1MB, beyond the 256KB tail window
		fmt.Fprintf(&b, `{"n":%d,"pad":"%s"}`+"\n", i, pad)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := util.TailJSONLLines(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	if !strings.HasPrefix(string(lines[2]), `{"n":999,`) {
		t.Fatalf("last line = %.40s", lines[2])
	}
	if !strings.HasPrefix(string(lines[0]), `{"n":997,`) {
		t.Fatalf("first tail line = %.40s", lines[0])
	}
}
