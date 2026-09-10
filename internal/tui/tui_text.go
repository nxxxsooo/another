package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/util"
)

// sessionCountText counts sessions in the reader's language, singular included.
func sessionCountText(n int) string {
	if n == 1 {
		return fmt.Sprintf(txt.sessionCountOneFmt, n)
	}
	return fmt.Sprintf(txt.sessionCountFmt, n)
}

func truncateDisplay(s string, n int) string {
	s = util.SanitizeDisplay(s)
	if s == "" {
		return txt.untitled
	}
	return ansi.Truncate(s, n, "…")
}

// elidePath shortens a path from the middle, keeping its root and as much of
// the tail as fits. Cut from the left instead, every path in a list of
// sibling projects began with the same "…" and lost the one segment that said
// which tree it was in: "…ents/sync/Docs/health" spends six cells proving that
// a word ends in "ents". "~/…/Docs/health" spends three and says where it is.
//
// It works on the already-tilde'd string and never touches the filesystem:
// this runs for every visible row on every keystroke.
func elidePath(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	root, rest, found := strings.Cut(s, "/")
	if !found {
		return truncateLeft(s, n)
	}
	// An absolute path cuts to an empty root, which is still the root: the
	// leading slash is what says the path did not start at home.
	parts := strings.Split(rest, "/")
	for i := 1; i < len(parts); i++ {
		if candidate := root + "/…/" + strings.Join(parts[i:], "/"); ansi.StringWidth(candidate) <= n {
			return candidate
		}
	}
	// Not even the root and the last segment fit; the tail is what identifies
	// the project, so it is the part that survives.
	return truncateLeft(s, n)
}

// truncateLeft keeps the tail of a path. The leading directories repeat across
// projects; the last segments are what identify one.
func truncateLeft(s string, n int) string {
	if ansi.StringWidth(s) <= n {
		return s
	}
	runes := []rune(s)
	for i := range runes {
		candidate := "…" + string(runes[i+1:])
		if ansi.StringWidth(candidate) <= n {
			return candidate
		}
	}
	return ansi.Truncate(s, n, "")
}

func padRight(s string, n int) string {
	if w := ansi.StringWidth(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func padLeft(s string, n int) string {
	if w := ansi.StringWidth(s); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
