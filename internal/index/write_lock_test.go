package index

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A refresh that overlaps another writer — the background content indexer, a
// second refresh, another another process — must wait for the write lock.
// A deferred transaction that reads first and then writes cannot: SQLite
// refuses the read-to-write upgrade with SQLITE_BUSY without consulting the
// busy timeout, which surfaced as "codex: database is locked (5)".
func TestReconcileWaitsForAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	ctx := context.Background()
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('holder','1')`); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	time.AfterFunc(300*time.Millisecond, func() {
		_, err := conn.ExecContext(ctx, `COMMIT`)
		released <- err
	})

	if err := store.reconcileProvider("codex", nil, map[string]struct{}{}); err != nil {
		t.Fatalf("reconcile while another writer held the lock: %v", err)
	}
	if err := <-released; err != nil {
		t.Fatal(err)
	}
}
