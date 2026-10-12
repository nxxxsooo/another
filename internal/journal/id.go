package journal

import (
	"crypto/rand"
	"encoding/hex"
)

// randomTail is six random bytes as hex, enough that two entries recorded
// inside the same millisecond stay ordered and distinct.
func randomTail() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failure here must not stop an action being recorded; a constant
		// tail keeps the id valid, only less unique.
		return "000000000000"
	}
	return hex.EncodeToString(b[:])
}
