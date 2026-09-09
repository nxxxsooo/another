//go:build !darwin

package codex

// desktopStateDir has no established location off macOS, which is the only
// platform Codex Desktop has been checked on.
//
// Guessing the macOS path here would answer "no lock, so Desktop is closed" on
// a machine where the lock simply lives somewhere else — and that answer is
// what decides whether another rewrites Desktop's entire persisted state
// underneath it. CODEX_DESKTOP_STATE_DIR remains the way to point another at a
// real one.
func desktopStateDir() (string, bool) { return "", false }
