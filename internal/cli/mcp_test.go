package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nxxxsooo/another/internal/herdr"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/mcpserver"
	"github.com/nxxxsooo/another/internal/util"
)

func TestSessionURIRoundTrips(t *testing.T) {
	for _, tc := range [][2]string{{"claude-code", "6517700b-2368"}, {"pi", "a b/c?d"}, {"opencode", "ses_01J#x"}} {
		p, id, ok := mcpserver.ParseSessionURI(mcpserver.SessionURI(tc[0], tc[1]))
		if !ok || p != tc[0] || id != tc[1] {
			t.Errorf("round trip %v = %q %q %v", tc, p, id, ok)
		}
	}
	for _, bad := range []string{"file:///etc/passwd", "another://other/pi/x", "another://session/pi", "another://session/pi/x/y", "another://session//x"} {
		if _, _, ok := mcpserver.ParseSessionURI(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestMCPWatchGroupGrantsAreLiveAndOpenCodeOnly(t *testing.T) {
	opencode, other := newFake(t, "opencode"), newFake(t, "other")
	base := t.TempDir()
	here, elsewhere := filepath.Join(base, "here"), filepath.Join(base, "elsewhere")
	for _, dir := range []string{here, elsewhere} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"ses_super", "ses_member", "ses_unrelated"} {
		opencode.seed(t, sampleConversation(id, elsewhere, id+" title"))
	}
	other.seed(t, sampleConversation("ses_member", elsewhere, "Different provider"))
	app := newTestApp(t, opencode, other)
	ctx := context.Background()
	if err := app.ensureIndex(ctx, "", true); err != nil {
		t.Fatal(err)
	}
	granted := true
	server, err := (&mcpserver.Server{Registry: app.Registry, Index: app.Index, Scope: util.DiscoverProjectScope(ctx, here), Version: "test", WatchStatus: func(context.Context) (herdr.WatchStatus, error) {
		if !granted {
			return herdr.WatchStatus{}, errors.New("discovery incomplete")
		}
		return herdr.WatchStatus{Groups: map[string][]string{"ses_super": {"ses_member"}}, Live: map[string]herdr.LiveSession{"ses_member": {Title: "Live title", State: "working", Terminal: "t2"}}}, nil
	}}).Build()
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	for _, id := range []string{"ses_super", "ses_member"} {
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.SessionURI("opencode", id)}); err != nil {
			t.Fatalf("group read %s: %v", id, err)
		}
	}
	for _, uri := range []string{mcpserver.SessionURI("opencode", "ses_unrelated"), mcpserver.SessionURI("other", "ses_member")} {
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Fatalf("unexpected grant: %s", uri)
		}
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "watch_status" {
		t.Fatalf("tools=%+v err=%v", tools, err)
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "watch_status", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("tool=%+v err=%v", result, err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Live title", "working", elsewhere, "opencode", "ses_member"} {
		if !strings.Contains(string(encoded), strings.Trim(string(mustJSON(t, text)), `"`)) {
			t.Fatalf("tool omitted %q: %s", text, encoded)
		}
	}
	granted = false
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.SessionURI("opencode", "ses_member")}); err == nil {
		t.Fatal("stale grant survived incomplete discovery")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The server lists what this project holds, across agents, and reads only
// that: a session from elsewhere is absent whether it is asked for by the
// listed URI form or not.
func TestMCPServerListsAndReadsOnlyThisProject(t *testing.T) {
	alpha, beta := newFake(t, "alpha"), newFake(t, "beta")
	base := t.TempDir()
	project := filepath.Join(base, "repo")
	child := filepath.Join(project, "packages", "api")
	sibling := filepath.Join(base, "repo-old")
	for _, dir := range []string{child, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alpha.seed(t, sampleConversation("here", project, "Here webhook"))
	beta.seed(t, sampleConversation("below", child, "Below webhook"))
	alpha.seed(t, sampleConversation("sibling", sibling, "Sibling webhook"))
	app := newTestApp(t, alpha, beta)
	ctx := context.Background()
	if err := app.ensureIndex(ctx, "", true); err != nil {
		t.Fatal(err)
	}

	server, err := (&mcpserver.Server{
		Registry: app.Registry, Index: app.Index, Version: "test",
		Scope: util.DiscoverProjectScope(ctx, project),
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	list, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Resources) == 2 && list.Resources[0].Name != "Here webhook · Fake alpha" {
		// Seeded with equal timestamps; the index breaks the tie the same
		// way every time, and the list must follow the index, not the URI.
		items, _ := app.Index.List(index.ListOpts{ProjectRoots: util.DiscoverProjectScope(ctx, project).Roots()})
		if list.Resources[0].URI != mcpserver.SessionURI(items[0].Provider, items[0].ID) {
			t.Fatalf("list order %v does not follow the index", list.Resources)
		}
	}
	var names []string
	for _, r := range list.Resources {
		names = append(names, r.Name)
		if r.MIMEType != "text/markdown" {
			t.Errorf("%s mime = %q", r.URI, r.MIMEType)
		}
	}
	sort.Strings(names)
	if strings.Join(names, "|") != "Below webhook · Fake beta|Here webhook · Fake alpha" {
		t.Fatalf("listed = %v", names)
	}
	for _, r := range list.Resources {
		if r.Name == "Below webhook · Fake beta" && !strings.Contains(r.Description, filepath.Join("packages", "api")) {
			t.Errorf("description should name the subdirectory: %q", r.Description)
		}
	}

	templates, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(templates.ResourceTemplates) != 1 || templates.ResourceTemplates[0].URITemplate != "another://session/{provider}/{id}" {
		t.Fatalf("templates = %+v, %v", templates, err)
	}

	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.SessionURI("beta", "below")})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) != 1 || !strings.Contains(res.Contents[0].Text, "# Below webhook") || !strings.Contains(res.Contents[0].Text, "ship it") {
		t.Fatalf("read = %+v", res.Contents)
	}

	for _, uri := range []string{mcpserver.SessionURI("alpha", "sibling"), mcpserver.SessionURI("alpha", "missing"), "another://session/alpha"} {
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read %s should fail as not found", uri)
		}
	}
}
