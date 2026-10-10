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

// pathDisplay expresses a path in "/" segments for the readers below and
// returns how to put the original separator back.
//
// Stored project paths carry the platform's own separator: a Windows session
// is recorded as C:\Users\... . Both of the elisions here are written in
// segments, and on a backslash path neither would find one — the whole path
// would come back as a single segment and be cut mid-word, which is the bug
// they exist to fix. The separator is translated rather than special-cased at
// every split.
func pathDisplay(s string) (string, func(string) string) {
	if strings.Contains(s, "\\") && !strings.Contains(s, "/") {
		return strings.ReplaceAll(s, "\\", "/"), func(out string) string {
			return strings.ReplaceAll(out, "/", "\\")
		}
	}
	return s, func(out string) string { return out }
}

// elidePath shortens a path from the middle, keeping its root and as much of
// the tail as fits. Cut from the left instead, every path began with the same
// "…" and lost the one segment that said which tree it was in:
// "…ents/sync/Docs/health" spends six cells proving that a word ends in
// "ents". "~/…/Docs/health" spends three and says where it is.
//
// This is the shortening for a path that is read once — a status line, the
// preview. A list of paths at the same depth is read down as well as across,
// and there a fixed tail reads better than a full one: see elidePathTail.
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
	work, restore := pathDisplay(s)
	root, rest, found := strings.Cut(work, "/")
	if !found {
		return restore(truncateLeft(work, n))
	}
	// An absolute path cuts to an empty root, which is still the root: the
	// leading slash is what says the path did not start at home.
	parts := strings.Split(rest, "/")
	for i := 1; i < len(parts); i++ {
		if candidate := root + "/…/" + strings.Join(parts[i:], "/"); ansi.StringWidth(candidate) <= n {
			return restore(candidate)
		}
	}
	// Not even the root and the last segment fit; the tail is what identifies
	// the project, so it is the part that survives.
	return restore(truncateLeft(work, n))
}

// elidePathTail is elidePath for a path with nothing above it to read against:
// the global column, where every row may come from anywhere on the machine.
//
// There the tail is all there is, and its depth is the preference pathTailDepth
// states — the last that many segments, whatever the path's own depth. The
// greedy form spent what the column could hold, which is honest and ragged:
// "~/…/sync/GitHub/another" beside "~/…/projects/smart-note" told one column of
// projects at two different depths, and the shorter row read as a different
// kind of thing rather than as a shorter path.
//
// No "…" marks the cut. In a column read this way almost every row is cut — a
// mark on nearly all of them is the "~/…/" this replaced, one cell shorter —
// and a path that is shallow enough to stand on its own already reads as one:
// "~/Documents" beside "sync/Tuning" says which of the two was shortened.
//
// When the window is wider than the column it is spent from its generic
// segments inward. The depth exists to keep the owner — "huatu/projects/
// smart-note" says whose project it is and "projects/smart-note" does not — so
// what goes first is the bucket, not the owner: length is the only signal a
// path offers, and the bucket is the long word of the pair. On a tie the parent
// stays, because it reads as the name's own prefix.
//
// A root — "~" or the empty segment an absolute path starts with — is a segment
// like any other here, which is what keeps it honest: it survives exactly when
// the window reaches it. "~/Documents/apps/x" shows "~" because the window
// still holds it, and "/opt/homebrew/var/log" does not show "/" because
// dropping "opt" left a path the root no longer describes.
func elidePathTail(s string, n, depth int) string {
	if n <= 0 {
		return ""
	}
	if depth < 1 {
		depth = 1
	}
	work, restore := pathDisplay(s)
	parts := strings.Split(work, "/")
	if last := len(parts) - 1; last >= 0 && parts[last] == "" {
		// A trailing separator is not a segment.
		parts = parts[:last]
	}
	if len(parts) == 0 {
		return ""
	}
	// What is read is the last depth segments and nothing above them, so a
	// path already that shallow keeps its own spelling, root and all.
	if len(parts) > depth {
		parts = parts[len(parts)-depth:]
	} else if ansi.StringWidth(work) <= n {
		return s
	}
	if joined := strings.Join(parts, "/"); ansi.StringWidth(joined) <= n {
		return restore(joined)
	}
	// Wider than the column: drop the widest segment above the name until it
	// fits. The name is what identifies the row, so it is the last to go.
	for len(parts) > 1 {
		drop := widestSegment(parts[:len(parts)-1])
		kept := make([]string, 0, len(parts)-1)
		kept = append(kept, parts[:drop]...)
		kept = append(kept, parts[drop+1:]...)
		parts = kept
		if joined := strings.Join(parts, "/"); ansi.StringWidth(joined) <= n {
			return restore(joined)
		}
	}
	return restore(truncateLeft(work, n))
}

// widestSegment is the index of the widest segment, and of the leftmost one
// when several are that wide: the leftmost is the one furthest from the name,
// so it is the one whose absence changes the least about how the row reads.
func widestSegment(parts []string) int {
	widest := 0
	for i, part := range parts {
		if ansi.StringWidth(part) > ansi.StringWidth(parts[widest]) {
			widest = i
		}
	}
	return widest
}

// pathTailDepth is how much of a globally-shown path survives by default: the
// name, the directory that holds it, and — because in a tree like this the
// directory that holds it is often a generic bucket such as "projects" — the
// owner above that, so "huatu/projects/smart-note" says whose project it is
// and "projects/smart-note" does not. It is a preference, not a constant a
// person has to live with: ui.path_depth states a different one, and a path
// column that is read all day is worth the cells it actually needs.
const pathTailDepth = 3

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
