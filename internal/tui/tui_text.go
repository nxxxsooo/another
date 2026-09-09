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
