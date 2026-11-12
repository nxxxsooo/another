package cli

import (
	"context"
	"testing"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
)

type readScanSpy struct {
	*fakeProvider
	scans, loads int
}

func (p *readScanSpy) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	p.scans++
	return p.fakeProvider.Discover(ctx, opts)
}
func (p *readScanSpy) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	p.loads++
	return p.fakeProvider.Load(ctx, ref)
}

func TestCachedReadCommandsNeverDiscoverOrIndexContent(t *testing.T) {
	p := &readScanSpy{fakeProvider: newFake(t, "alpha")}
	p.seed(t, sampleConversation("cached", t.TempDir(), "Cached title"))
	app := newTestApp(t, p)
	if err := app.ensureIndex(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	p.scans, p.loads = 0, 0
	if err := app.Index.SetMeta("last_update", ""); err != nil {
		t.Fatal(err)
	}
	mustRun(t, app, "list", "--cached", "--all", "--json")
	mustRun(t, app, "search", "Cached", "--cached", "--all", "--json")
	if p.scans != 0 || p.loads != 0 {
		t.Fatalf("cached reads did I/O: scans=%d loads=%d", p.scans, p.loads)
	}
	mustRun(t, app, "context", "cached", "--provider", "alpha", "--cached", "--all")
	if p.scans != 0 || p.loads != 1 {
		t.Fatalf("context should load only the requested session: scans=%d loads=%d", p.scans, p.loads)
	}
	for _, args := range [][]string{{"list", "--cached", "--refresh"}, {"search", "Cached", "--cached", "--refresh"}, {"context", "cached", "--cached", "--refresh"}} {
		_, err := run(t, app, args...)
		wantErr(t, err, "--cached and --refresh")
	}
}

func TestProviderSpecificReadDoesNotDiscoverOtherProviders(t *testing.T) {
	a, b := &readScanSpy{fakeProvider: newFake(t, "alpha")}, &readScanSpy{fakeProvider: newFake(t, "beta")}
	a.seed(t, sampleConversation("one", t.TempDir(), "One"))
	app := newTestApp(t, a, b)
	if err := app.ensureIndex(context.Background(), "alpha", true); err != nil {
		t.Fatal(err)
	}
	if a.scans != 1 || b.scans != 0 {
		t.Fatalf("provider filter not respected: %d %d", a.scans, b.scans)
	}
	if done, err := app.Index.GetMeta("titler_content_prune_v1"); err != nil || done != "" {
		t.Fatalf("provider-only scan incorrectly settled global cleanup: %q %v", done, err)
	}
}
