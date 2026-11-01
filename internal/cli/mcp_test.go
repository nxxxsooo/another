package cli

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
