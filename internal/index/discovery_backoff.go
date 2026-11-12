package index

import (
	"context"
	"time"

	"github.com/nxxxsooo/another/internal/provider"
)

// Remote discovery failures must survive CLI process boundaries. Otherwise an
// enabled, installed but unreadable cloud store with zero indexed rows makes
// every read appear to need a first scan. Never reconcile away its cached rows.
const discoveryRetryDelay = time.Minute
const discoveryRetryPrefix = "discovery_retry:"

func remoteDiscovery(p provider.Provider) bool {
	r, ok := p.(interface{ RequiresRemoteDiscovery() bool })
	return ok && r.RequiresRemoteDiscovery()
}

func discoveryBackingOff(p provider.Provider, store *Store) bool {
	if !remoteDiscovery(p) {
		return false
	}
	raw, err := store.GetMeta(discoveryRetryPrefix + p.ID())
	if err != nil {
		return false
	}
	until, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && time.Now().Before(until)
}

func rememberDiscoveryFailure(ctx context.Context, p provider.Provider, store *Store) {
	if remoteDiscovery(p) && ctx.Err() == nil {
		_ = store.SetMeta(discoveryRetryPrefix+p.ID(), time.Now().Add(discoveryRetryDelay).UTC().Format(time.RFC3339Nano))
	}
}

// ResetDiscoveryBackoff lets an explicit --refresh retry immediately. Rebuild
// already probes unconditionally. Empty providerID resets all retry deadlines.
func (s *Store) ResetDiscoveryBackoff(providerID string) error {
	if providerID != "" {
		_, err := s.db.Exec(`DELETE FROM meta WHERE key = ?`, discoveryRetryPrefix+providerID)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM meta WHERE substr(key,1,?) = ?`, len(discoveryRetryPrefix), discoveryRetryPrefix)
	return err
}
