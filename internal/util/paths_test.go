package util_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyrusSE/agenthop/internal/util"
)

func TestReadJSONLLinesAcceptsLargeProviderRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	want := strings.Repeat("x", 9*1024*1024)
	if err := os.WriteFile(path, []byte(want+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got int
	if err := util.ReadJSONLLines(path, 0, func(line []byte) error {
		got = len(line)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != len(want) {
		t.Fatalf("record length = %d, want %d", got, len(want))
	}
}

func TestEncodeClaudeProjectPath(t *testing.T) {
	got := util.EncodeClaudeProjectPath("/home/user/web_root/a.b c")
	want := "-home-user-web-root-a-b-c"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	if got, want := util.ShellQuote("/tmp/a b's"), `'`+`/tmp/a b`+`'"'"'`+`s`+`'`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDecodeCursorProjectPath(t *testing.T) {
	got := util.DecodeCursorProjectPath("home-cyrus-Documents-test-miggrate")
	want := "/home/cyrus/Documents/test/miggrate"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMatchID(t *testing.T) {
	id := "01234567-89ab-cdef-0123-456789abcdef"
	if !util.MatchID(id, "89abcdef") {
		t.Fatal("suffix match failed")
	}
	if !util.MatchID(id, id) {
		t.Fatal("exact match failed")
	}
}

func TestProjectPathMatchesCWD(t *testing.T) {
	home := util.HomeDir()
	if home == "" {
		t.Skip("no home dir")
	}
	proj := filepath.Join(home, "proj")
	sub := filepath.Join(proj, "sub")
	if util.ProjectPathMatchesCWD(home, home) {
		t.Fatal("exact home path should not match home filter")
	}
	if !util.ProjectPathMatchesCWD(proj, home) {
		t.Fatal("project under home should match home filter")
	}
	if util.ProjectPathMatchesCWD(sub, proj) {
		t.Fatal("subdir should not match exact project filter")
	}
	if util.ProjectPathMatchesCWD("/other", proj) {
		t.Fatal("unrelated path should not match")
	}
}

func TestProjectPathMatchesCWDExact(t *testing.T) {
	home := util.HomeDir()
	if home == "" {
		t.Skip("no home dir")
	}
	proj := filepath.Join(home, "proj")
	if !util.ProjectPathMatchesCWD(proj, proj) {
		t.Fatal("exact path should match")
	}
}

func TestWriteFileAtomicIsSecureAndCleansFailedTemp(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	dst := filepath.Join(dir, "target")
	if err := os.WriteFile(victim, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, dst); err != nil {
		t.Fatal(err)
	}
	if err := util.WriteFileAtomic(dst, []byte("replacement"), 0o640); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "untouched" {
		t.Fatalf("destination symlink was followed: %q", got)
	}
	if got, _ := os.ReadFile(dst); string(got) != "replacement" {
		t.Fatalf("target = %q", got)
	}
	if info, err := os.Stat(dst); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o640 {
		t.Fatalf("target mode = %v", info.Mode().Perm())
	}
	if err := util.WriteFileAtomic(dir, []byte("cannot replace directory"), 0o600); err == nil {
		t.Fatal("expected rename over directory to fail")
	}
	if temps, err := filepath.Glob(filepath.Join(dir, ".agenthop-*.tmp")); err != nil || len(temps) != 0 {
		t.Fatalf("temporary files remain: %v err=%v", temps, err)
	}
}
