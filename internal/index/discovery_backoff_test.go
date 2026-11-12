package index_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

type remoteScanSpy struct {
	*stubProvider
	calls int
}

func (p *remoteScanSpy) RequiresRemoteDiscovery() bool { return true }
func (p *remoteScanSpy) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	p.calls++
	return p.stubProvider.Discover(ctx, opts)
}

func TestRemoteDiscoveryFailureBackoffPersistsAndExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	store, err := index.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := &remoteScanSpy{stubProvider: &stubProvider{id: "doubao", err: errors.New("invalid Doubao response")}}
	reg := registry.NewWith(p)
	if _, err := index.UpdateIncremental(context.Background(), reg, store, ""); err == nil {
		t.Fatal("first failure was not reported")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = index.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if index.NeedsIncrementalIndex(reg, store, time.Minute) {
		t.Fatal("failed remote's missing rows/metadata forced another scan")
	}
	if _, err := index.UpdateIncremental(context.Background(), reg, store, ""); err != nil || p.calls != 1 {
		t.Fatalf("cached failure probed again: calls=%d err=%v", p.calls, err)
	}
	if err := store.SetMeta("discovery_retry:doubao", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if !index.NeedsIncrementalIndex(reg, store, time.Minute) {
		t.Fatal("expired failure did not become retryable")
	}
	_, _ = index.UpdateIncremental(context.Background(), reg, store, "")
	if p.calls != 2 {
		t.Fatal(p.calls)
	}
	if err := store.ResetDiscoveryBackoff("doubao"); err != nil {
		t.Fatal(err)
	}
	_, _ = index.UpdateIncremental(context.Background(), reg, store, "")
	if p.calls != 3 {
		t.Fatal("explicit refresh did not retry")
	}
	p.err = nil
	if _, err := index.Rebuild(context.Background(), reg, store, ""); err != nil || p.calls != 4 {
		t.Fatalf("rebuild did not bypass backoff: %v calls=%d", err, p.calls)
	}
	if index.NeedsIncrementalIndex(reg, store, time.Minute) {
		t.Fatal("successful empty remote scan was treated as never indexed")
	}
}

func TestFailedRemoteDiscoveryPreservesCachedRows(t *testing.T) {
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Upsert(model.Summary{ID: "existing", Provider: "doubao", StoragePath: filepath.Join(t.TempDir(), "native")}); err != nil {
		t.Fatal(err)
	}
	p := &remoteScanSpy{stubProvider: &stubProvider{id: "doubao", err: errors.New("offline")}}
	for i := 0; i < 2; i++ {
		_, _ = index.UpdateIncremental(context.Background(), registry.NewWith(p), store, "")
	}
	if _, err := store.Get("doubao", "existing"); err != nil {
		t.Fatalf("failed discovery reconciled cached data away: %v", err)
	}
	if p.calls != 1 {
		t.Fatal(p.calls)
	}
}
