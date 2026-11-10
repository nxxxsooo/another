package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
)

func writeProjectRollout(t *testing.T, path, cwd string, source any) {
	t.Helper()
	meta, err := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": nativeTitleTestID, "cwd": cwd, "source": source}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(meta, []byte("\n"+`{"type":"event_msg","payload":{"type":"user_message","message":"Guardian review"}}`+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGuardianMetadataOverridesNativeAndLegacyVisibility(t *testing.T) {
	for _, catalog := range []string{"native", "index", "desktop"} {
		t.Run(catalog, func(t *testing.T) {
			p, db, ref := nativeTitleFixture(t)
			if catalog != "native" {
				if _, err := db.Exec(`DROP TABLE threads`); err != nil {
					t.Fatal(err)
				}
			}
			if catalog == "desktop" {
				if err := os.Remove(filepath.Join(filepath.Dir(p.sessionsRoot), "session_index.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			for _, guardian := range []bool{true, false} {
				var source any = "cli"
				if guardian {
					source = map[string]any{"subagent": map[string]any{"other": "guardian"}}
				}
				writeProjectRollout(t, ref.StoragePath, t.TempDir(), source)
				sm, err := p.SummarizeFile(ref.StoragePath)
				if err != nil {
					t.Fatal(err)
				}
				want := model.SessionKindRoot
				if guardian {
					want = model.SessionKindSubagent
				}
				if sm.Kind != want {
					t.Fatalf("guardian=%v: kind=%q want=%q", guardian, sm.Kind, want)
				}
			}
			writeProjectRollout(t, ref.StoragePath, t.TempDir(), map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "parent"}}})
			sm, err := p.SummarizeFile(ref.StoragePath)
			if err != nil || sm.Kind != model.SessionKindRoot || sm.ParentID != "" {
				t.Fatalf("named ordinary subagent promotion changed: %+v %v", sm, err)
			}
		})
	}
	// A user title mentioning Guardian has no classification power.
	p, db, ref := nativeTitleFixture(t)
	if _, err := db.Exec(`UPDATE threads SET name = 'Guardian review'`); err != nil {
		t.Fatal(err)
	}
	sm, err := p.SummarizeFile(ref.StoragePath)
	if err != nil || sm.Kind != model.SessionKindRoot {
		t.Fatalf("ordinary title: %+v %v", sm, err)
	}
}

func TestProjectLabelsNativePriorityAndBoundaries(t *testing.T) {
	p, db, ref := nativeTitleFixture(t)
	home := filepath.Dir(p.sessionsRoot)
	root := filepath.Join(home, ".chatgpt-projects", "g-p-synthetic")
	outside := t.TempDir()
	if _, err := db.Exec(`CREATE TABLE projects (id TEXT, name TEXT); CREATE TABLE project_roots (project_id TEXT, path TEXT); INSERT INTO projects VALUES ('p', 'Native Garden')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project_roots VALUES ('p', ?), ('p', ?)`, root, outside); err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(map[string]any{"local-projects": map[string]any{"p": map[string]any{"id": "p", "name": "Desktop Garden", "rootPaths": []string{root}}}})
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ cwd, want string }{
		{root, "Native Garden"},
		{filepath.Join(root, "src", "UI"), filepath.Join("Native Garden", "src", "UI")},
		{root + "-sibling", ""},
		{filepath.Join(root, "..", "g-p-other"), ""},
		{outside, ""},
	} {
		writeProjectRollout(t, ref.StoragePath, tc.cwd, "cli")
		sm, err := p.SummarizeFile(ref.StoragePath)
		if err != nil || sm.ProjectLabel != tc.want || sm.ProjectPath != tc.cwd {
			t.Fatalf("cwd %q: %+v %v, want %q", tc.cwd, sm, err, tc.want)
		}
	}
	if _, err := db.Exec(`INSERT INTO projects VALUES ('q', 'Conflicting Garden'); INSERT INTO project_roots VALUES ('q', ?)`, root); err != nil {
		t.Fatal(err)
	}
	writeProjectRollout(t, ref.StoragePath, root, "cli")
	sm, err := p.SummarizeFile(ref.StoragePath)
	if err != nil || sm.ProjectLabel != "" {
		t.Fatalf("ambiguous native root used fallback: %+v %v", sm, err)
	}
	if _, err := db.Exec(`DROP TABLE project_roots`); err != nil {
		t.Fatal(err)
	}
	sm, err = p.SummarizeFile(ref.StoragePath)
	if err != nil || sm.ProjectLabel != "Desktop Garden" {
		t.Fatalf("desktop fallback: %+v %v", sm, err)
	}
}

func TestDesktopProjectLabelsMalformedAndMissing(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".chatgpt-projects", "g-p-synthetic")
	for _, tc := range []struct{ state, want string }{
		{`{}`, ""}, {`not json`, ""},
		{`{"local-projects":[]}`, ""},
		{`{"local-projects":{"p":{"id":"p","name":42,"rootPaths":["ROOT"]}}}`, ""},
		{`{"local-projects":{"p":{"id":"p","name":"Good","rootPaths":["ROOT"]},"bad":{"name":42}}}`, "Good"},
		{`{"local-projects":{"p":{"id":"p","name":"Good","rootPaths":["ROOT"]},"q":{"id":"q","name":"Other","rootPaths":["ROOT"]}}}`, ""},
		{`{"local-projects":{"p":{"id":"p","name":"Bad\nName","rootPaths":["ROOT"]}}}`, ""},
	} {
		encoded, _ := json.Marshal(root)
		p := newProjectLabels(home)
		p.loadDesktop([]byte(strings.ReplaceAll(tc.state, `"ROOT"`, string(encoded))))
		if got := p.label(root); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.state, got, tc.want)
		}
	}
}

func TestProjectLabelsUnsupportedOptionalSchema(t *testing.T) {
	p, db, ref := nativeTitleFixture(t)
	root := filepath.Join(filepath.Dir(p.sessionsRoot), ".chatgpt-projects", "g-p-synthetic")
	writeProjectRollout(t, ref.StoragePath, root, "cli")
	for _, schema := range []string{
		`CREATE TABLE projects (id TEXT, metadata TEXT)`,
		`DROP TABLE projects; CREATE TABLE projects (id TEXT, name TEXT); CREATE TABLE project_roots (project_id TEXT, position INTEGER)`,
	} {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
		sm, err := p.SummarizeFile(ref.StoragePath)
		if err != nil || sm.ProjectLabel != "" || sm.ProjectPath != root {
			t.Fatalf("unsupported schema: %+v %v", sm, err)
		}
	}
}

func TestProjectMetadataInvalidatesFingerprint(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "native", false: "desktop"}[native], func(t *testing.T) {
			p, db, ref := nativeTitleFixture(t)
			root := filepath.Join(filepath.Dir(p.sessionsRoot), ".chatgpt-projects", "g-p-synthetic")
			writeProjectRollout(t, ref.StoragePath, root, "cli")
			if _, err := db.Exec(`CREATE TABLE projects (id TEXT, name TEXT); CREATE TABLE project_roots (project_id TEXT, path TEXT)`); err != nil {
				t.Fatal(err)
			}
			set := func(name string) {
				t.Helper()
				if native {
					if _, err := db.Exec(`DELETE FROM projects; DELETE FROM project_roots`); err != nil {
						t.Fatal(err)
					}
					if name != "" {
						if _, err := db.Exec(`INSERT INTO projects VALUES ('p', ?)`, name); err != nil {
							t.Fatal(err)
						}
						if _, err := db.Exec(`INSERT INTO project_roots VALUES ('p', ?)`, root); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					projects := map[string]any{}
					if name != "" {
						projects["p"] = map[string]any{"id": "p", "name": name, "rootPaths": []string{root}}
					}
					data, _ := json.Marshal(map[string]any{"local-projects": projects})
					if err := os.WriteFile(filepath.Join(filepath.Dir(p.sessionsRoot), ".codex-global-state.json"), data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			var previous model.Summary
			for _, name := range []string{"", "First Garden", "Second Garden", ""} {
				set(name)
				sums, err := p.Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(_ string, m, s int64) bool { return m == previous.SourceMtime && s == previous.SourceSize }})
				if err != nil || len(sums) != 1 {
					t.Fatalf("metadata %q skipped: %v %v", name, sums, err)
				}
				if sums[0].ProjectLabel != name {
					t.Fatalf("got %q want %q", sums[0].ProjectLabel, name)
				}
				previous = sums[0]
				if _, err := db.Exec(`UPDATE threads SET updated_at = updated_at + 1`); err != nil {
					t.Fatal(err)
				}
				unchanged, err := p.Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(_ string, m, s int64) bool { return m == previous.SourceMtime && s == previous.SourceSize }})
				if err != nil || len(unchanged) != 0 {
					t.Fatalf("unrelated write invalidated: %v %v", unchanged, err)
				}
			}
		})
	}
}

func TestWindowsProjectLabelsExtendedAndCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	home := t.TempDir()
	root := filepath.Join(home, ".chatgpt-projects", "g-p-synthetic")
	p := newProjectLabels(home)
	p.roots[p.root(`\\?\`+root)] = "Garden"
	if got := p.label(filepath.Join(strings.ToUpper(root), "Child")); got != filepath.Join("Garden", "Child") {
		t.Fatalf("extended/case label = %q", got)
	}
}
