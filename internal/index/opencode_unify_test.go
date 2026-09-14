package index_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/nxxxsooo/another/internal/index"
	_ "modernc.org/sqlite"
)

func TestOpeningIndexUnifiesFormerOpenCodeRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	store, err := index.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO sessions
 (id,provider,project_path,title,created_at,updated_at,message_count,storage_path,source_mtime,kind,parent_id,source_size)
 VALUES ('old','opencode2','','old',1,1,1,'/tmp/opencode2.db#old',1,'root','',1)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = index.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rows, err := store.List(index.ListOpts{Provider: "opencode", IncludeSubagents: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Provider != "opencode" || rows[0].ID != "old" {
		t.Fatalf("rows = %+v", rows)
	}
}
