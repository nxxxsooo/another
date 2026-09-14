package opencodeunified

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	_ "modernc.org/sqlite"
)

type fakeBackend struct {
	id, path         string
	rows             []model.Summary
	loaded           map[string]bool
	resume           string
	renamed, deleted string
	relocate         bool
}

func TestDiscoversV1HistoryBesideEarlyIsolatedV2Store(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	standard := filepath.Join(root, "opencode", "opencode.db")
	isolated := filepath.Join(root, "opencode", "opencode2.db")
	if err := createStore(standard, `
CREATE TABLE session (id TEXT, directory TEXT, title TEXT, time_created INTEGER, time_updated INTEGER, parent_id TEXT, metadata TEXT, time_archived INTEGER);
CREATE TABLE message (id TEXT, session_id TEXT, data TEXT, time_created INTEGER);
CREATE TABLE part (id TEXT, message_id TEXT, data TEXT, time_created INTEGER);
INSERT INTO session VALUES ('v1','/project','V1 history',1000,2000,NULL,NULL,NULL);
INSERT INTO message VALUES ('m1','v1','{"role":"user"}',1000);
INSERT INTO part VALUES ('p1','m1','{"type":"text","text":"legacy"}',1000);`); err != nil {
		t.Fatal(err)
	}
	if err := createStore(isolated, `
CREATE TABLE session_v2 (id TEXT, project_id TEXT, parent_id TEXT, slug TEXT, directory TEXT, title TEXT, version TEXT, share_url TEXT, agent TEXT, model TEXT, metadata TEXT, time_created INTEGER, time_updated INTEGER);
CREATE TABLE session_message (session_id TEXT, seq INTEGER, id TEXT, type TEXT, time_created INTEGER, data TEXT);
INSERT INTO session_v2 VALUES ('v2','project',NULL,'v2','/project','V2 session','2.0',NULL,'build','{}',NULL,1000,3000);
INSERT INTO session_message VALUES ('v2',0,'m2','user',1000,'{"text":"current"}');`); err != nil {
		t.Fatal(err)
	}

	rows, err := New().Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("sessions = %+v", rows)
	}
	got := map[string]string{}
	for _, row := range rows {
		got[row.ID] = row.Provider
	}
	if !reflect.DeepEqual(got, map[string]string{"v1": "opencode", "v2": "opencode"}) {
		t.Fatalf("sessions = %+v", rows)
	}
}

func createStore(path, schema string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(schema)
	return err
}

func (f *fakeBackend) ID() string                        { return f.id }
func (f *fakeBackend) DisplayName() string               { return f.id }
func (f *fakeBackend) DefaultPaths() []provider.PathSpec { return []provider.PathSpec{{Path: f.path}} }
func (f *fakeBackend) Installed() bool                   { return true }
func (f *fakeBackend) Discover(context.Context, provider.DiscoverOpts) ([]model.Summary, error) {
	return f.rows, nil
}
func (f *fakeBackend) Load(_ context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	if !f.loaded[ref.ID] {
		return nil, provider.ErrNotFound
	}
	return &model.Conversation{ID: ref.ID, Provider: f.id, StoragePath: f.path + "#" + ref.ID, Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}, nil
}
func (f *fakeBackend) Write(context.Context, *model.Conversation, provider.WriteOpts) (*provider.WriteResult, error) {
	return nil, errors.New("unused")
}
func (f *fakeBackend) SupportsResume() bool                      { return true }
func (f *fakeBackend) ResumeCommand(provider.WriteResult) string { return f.resume }
func (f *fakeBackend) RenameSession(_ context.Context, ref provider.SessionRef, title string) error {
	f.renamed = ref.ID + ":" + title
	return nil
}
func (f *fakeBackend) DeleteSession(_ context.Context, ref provider.SessionRef) error {
	f.deleted = ref.ID
	return nil
}
func (f *fakeBackend) SupportsRelocate(provider.RelocateMode) bool { return f.relocate }
func (f *fakeBackend) RelocateSession(_ context.Context, ref provider.SessionRef, opts provider.RelocateOpts) (*provider.RelocateResult, error) {
	if !f.relocate {
		return nil, provider.ErrRelocateUnsupported
	}
	return &provider.RelocateResult{SessionID: ref.ID, ProjectPath: opts.Directory}, nil
}

func TestUnifiedDiscoveryAndNativeRouting(t *testing.T) {
	v2 := &fakeBackend{id: "opencode2", path: "/data/opencode.db", loaded: map[string]bool{"same": true, "v2": true}, resume: "opencode", relocate: true,
		rows: []model.Summary{{ID: "same", Provider: "opencode2", StoragePath: "/data/opencode.db#same"}, {ID: "v2", Provider: "opencode2", StoragePath: "/data/opencode.db#v2"}}}
	v1 := &fakeBackend{id: "opencode", path: "/data/opencode.db", loaded: map[string]bool{"same": true, "v1": true}, resume: "legacy",
		rows: []model.Summary{{ID: "same", Provider: "opencode", StoragePath: "/data/opencode.db#same"}, {ID: "v1", Provider: "opencode", StoragePath: "/data/opencode.db#v1"}}}
	p := &Provider{backends: []backend{{p: v2, path: v2.path, v2: true, primary: true, command: "sh"}, {p: v1, path: v1.path, command: "sh"}}}
	rows, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{rows[0].ID, rows[1].ID, rows[2].ID}
	if !reflect.DeepEqual(got, []string{"same", "v2", "v1"}) {
		t.Fatalf("sessions = %v", got)
	}
	for _, row := range rows {
		if row.Provider != ProviderID {
			t.Fatalf("provider = %q", row.Provider)
		}
	}

	ref := provider.SessionRef{ID: "v1", StoragePath: "/data/opencode.db#v1"}
	if err := p.RenameSession(context.Background(), ref, "old title"); err != nil {
		t.Fatal(err)
	}
	if v1.renamed != "v1:old title" || v2.renamed != "" {
		t.Fatalf("rename routed v1=%q v2=%q", v1.renamed, v2.renamed)
	}
	if caps := p.Capabilities(ref); !caps.Rename || !caps.Delete || caps.RelocateFork {
		t.Fatalf("V1 caps = %+v", caps)
	}
	if caps := p.Capabilities(provider.SessionRef{ID: "v2", StoragePath: "/data/opencode.db#v2"}); !caps.RelocateFork || !caps.RelocateMove {
		t.Fatalf("V2 caps = %+v", caps)
	}
}

func TestFormerIsolatedV2KeepsItsResumeCommand(t *testing.T) {
	official := &fakeBackend{id: "opencode2", path: "/data/opencode.db", loaded: map[string]bool{}, resume: "opencode"}
	isolated := &fakeBackend{id: "opencode2", path: "/data/opencode2.db", loaded: map[string]bool{"old-v2": true}, resume: "opencode2"}
	p := &Provider{backends: []backend{{p: official, path: official.path, v2: true, primary: true, command: "sh"}, {p: isolated, path: isolated.path, v2: true, command: "sh"}}}
	got := p.ResumeCommand(provider.WriteResult{SessionID: "old-v2", StoragePath: "/data/opencode2.db#old-v2"})
	if got != "opencode2" {
		t.Fatalf("resume = %q", got)
	}
}

func TestIsolatedV2WithoutItsCommandIsHistoryOnly(t *testing.T) {
	isolated := &fakeBackend{id: "opencode2", path: "/data/opencode2.db", loaded: map[string]bool{"old-v2": true}, resume: "missing"}
	p := &Provider{backends: []backend{{p: isolated, path: isolated.path, v2: true, command: "definitely-not-an-opencode-command"}}}
	ref := provider.SessionRef{ID: "old-v2", StoragePath: "/data/opencode2.db#v2:old-v2"}
	if caps := p.Capabilities(ref); caps.Rename || caps.Delete || caps.RelocateFork {
		t.Fatalf("unavailable CLI capabilities = %+v", caps)
	}
	if reason := p.ResumeUnavailableReason(provider.WriteResult{SessionID: ref.ID, StoragePath: ref.StoragePath}); reason == "" {
		t.Fatal("missing compatibility command did not block resume")
	}
}
