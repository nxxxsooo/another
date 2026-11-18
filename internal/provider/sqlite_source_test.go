package provider

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSQLiteSourceStampFollowsDatabaseSymlinkToWAL(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "native.db")
	alias := filepath.Join(dir, "alias.db")
	if err := os.WriteFile(db, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(db), alias); err != nil {
		if runtime.GOOS == "windows" && os.IsPermission(err) {
			t.Skipf("Windows requires symlink privileges: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.WriteFile(alias+"-wal", []byte("stale alias"), 0o600); err != nil {
		t.Fatal(err)
	}
	main, err := os.Stat(alias)
	if err != nil {
		t.Fatal(err)
	}
	before, beforeSize, err := SQLiteSourceStamp(alias, main)
	if err != nil || !before.Equal(main.ModTime()) || beforeSize != main.Size() {
		t.Fatalf("stamp without real WAL = (%v, %d, %v)", before, beforeSize, err)
	}
	wal := db + "-wal"
	if err := os.WriteFile(wal, []byte("wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := main.ModTime().Truncate(time.Second).Add(2 * time.Second)
	if err := os.Chtimes(wal, want, want); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{db, alias} {
		stamp, size, err := SQLiteSourceStamp(path, main)
		if err != nil || !stamp.Equal(want) || size != main.Size()+3 {
			t.Fatalf("%s stamp with real WAL = (%v, %d, %v)", path, stamp, size, err)
		}
	}
}
