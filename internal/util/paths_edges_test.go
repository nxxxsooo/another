package util_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyrusSE/agenthop/internal/util"
)

func TestScanJSONLEdgesFindsTailMeta(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, `{"type":"event_msg","n":`+string(rune('0'+i%10))+`}`)
	}
	lines = append(lines, `{"type":"agenthop_migration","data":{"originDigest":"abc123"}}`)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found := util.ScanJSONLEdges(path, 3, 4096, func(line []byte) bool {
		return strings.Contains(string(line), "abc123")
	})
	if !found {
		t.Fatal("expected tail migration line to match")
	}
}

func TestReadJSONLPrefixBoundsLargeRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	data := `{"type":"response_item","payload":"` + strings.Repeat("x", 2<<20) + `"}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	var got int
	if err := util.ReadJSONLPrefix(path, 64<<10, 10, func(line []byte) error {
		got = len(line)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got == 0 || got > 64<<10 {
		t.Fatalf("callback bytes = %d, want 1..65536", got)
	}
}
