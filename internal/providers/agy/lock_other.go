//go:build !darwin && !linux

package agy

import "fmt"

// CleanupWrite remains available everywhere because it only rolls back a fresh
// migration. Renaming and deleting an existing conversation are rejected where
// AGY's presence lock cannot yet be held for the complete mutation, since
// neither can tell a conversation AGY has open from one it does not.
func acquireConversationLock(string) (func() error, bool, error) {
	return nil, false, fmt.Errorf("agy: changing an existing conversation is not supported on this platform")
}

// describeLockHolder has nothing to report where the lock itself is unavailable.
func describeLockHolder(string) string { return "" }
