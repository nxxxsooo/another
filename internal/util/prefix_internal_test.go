package util

import (
	"runtime"
	"testing"
)

// EvalSymlinks on Windows returns \\?\C:\... for an existing directory while
// a deleted one keeps the plain C:\... form (resolution fails and the raw
// absolute path is kept). Stored and queried project paths then stop matching
// for no reason the user can see. The pure mapping is tested directly because
// the platform branch cannot execute elsewhere.
func TestStripExtendedVolumePrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{`\\?\C:\Users\a\proj`, `C:\Users\a\proj`},
		{`\\?\UNC\server\share\proj`, `\\server\share\proj`},
		{`C:\Users\a\proj`, `C:\Users\a\proj`},
		{`/home/a/proj`, `/home/a/proj`},
		{``, ``},
	}
	for _, tc := range cases {
		if got := stripExtendedVolumePrefix(tc.in); got != tc.want {
			t.Fatalf("stripExtendedVolumePrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStripExtendedPathPrefixIsIdentityOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the passthrough only exists elsewhere")
	}
	for _, p := range []string{`\\?\C:\x`, `/home/a`, ``} {
		if got := stripExtendedPathPrefix(p); got != p {
			t.Fatalf("stripExtendedPathPrefix(%q) = %q off Windows", p, got)
		}
	}
}
