// Package recall renders a session as Markdown another agent can read: the
// bounded view a mention puts into a prompt, and the full transcript an export
// writes. It is pure and deterministic; loading and scoping belong to callers.
package recall

import (
	"fmt"
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/util"
)

// Bounds limits what a rendering carries. Zero fields mean no limit.
type Bounds struct {
	Head       int // opening turns kept: they say what the work set out to do
	Tail       int // closing turns kept: they say where it ended up
	PerMessage int // runes per turn
	Total      int // runes for the whole document
}

// Mention is the view a mentioned session puts into a prompt. Nine turns of at
// most 2,000 runes fit under the total with room for the header, so the total
// only bites on sessions whose header or elision lines run long.
var Mention = Bounds{Head: 3, Tail: 6, PerMessage: 2000, Total: 24000}

// Full renders every cleaned turn.
var Full = Bounds{}

// Source names the session for the header; the conversation alone does not
// carry the agent's display name.
type Source struct {
	AgentName string
}

type turn struct {
	index int
	role  model.Role
	when  time.Time
	text  string
}

// Render writes conv as Markdown within b.
func Render(conv *model.Conversation, src Source, b Bounds) string {
	turns := cleanTurns(conv.Messages, b.PerMessage)
	kept := selectTurns(len(turns), b)
	body := renderBody(turns, kept)
	header := renderHeader(conv, src, len(turns), len(kept), b)
	for b.Total > 0 && runeLen(header)+runeLen(body) > b.Total && len(kept) > 1 {
		kept = dropOne(kept)
		body = renderBody(turns, kept)
		header = renderHeader(conv, src, len(turns), len(kept), b)
	}
	return header + body
}

func cleanTurns(msgs []model.Message, perMessage int) []turn {
	var out []turn
	for _, m := range msgs {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		text := m.PlainText()
		if m.Role == model.RoleUser {
			text = util.DisplayUserText(text)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		out = append(out, turn{index: len(out) + 1, role: m.Role, when: m.Timestamp, text: clip(text, perMessage)})
	}
	return out
}

// selectTurns picks indexes into the turn list: all of them, or the head and
// the tail when the session is longer than both together.
func selectTurns(n int, b Bounds) []int {
	if b.Head <= 0 && b.Tail <= 0 || n <= b.Head+b.Tail {
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		return all
	}
	var kept []int
	for i := 0; i < b.Head; i++ {
		kept = append(kept, i)
	}
	for i := n - b.Tail; i < n; i++ {
		kept = append(kept, i)
	}
	return kept
}

// dropOne gives up the turn that says least: the oldest of the closing turns,
// then the latest of the opening ones. The first turn, which carries the
// request, and the last, which carries the outcome, go last of all.
func dropOne(kept []int) []int {
	gap := len(kept)
	for i := 1; i < len(kept); i++ {
		if kept[i] != kept[i-1]+1 {
			gap = i
			break
		}
	}
	switch {
	case gap < len(kept)-1:
		return append(append([]int(nil), kept[:gap]...), kept[gap+1:]...)
	case gap > 1 && gap < len(kept):
		return append(append([]int(nil), kept[:gap-1]...), kept[gap:]...)
	case len(kept) > 2:
		mid := len(kept) / 2
		return append(append([]int(nil), kept[:mid]...), kept[mid+1:]...)
	default:
		return kept[1:]
	}
}

func renderHeader(conv *model.Conversation, src Source, total, shown int, b Bounds) string {
	var sb strings.Builder
	title := strings.TrimSpace(conv.Title)
	if title == "" {
		title = "Untitled session"
	}
	fmt.Fprintf(&sb, "# %s\n\n", title)
	agent := conv.Provider
	if src.AgentName != "" && src.AgentName != conv.Provider {
		agent = fmt.Sprintf("%s (`%s`)", src.AgentName, conv.Provider)
	}
	fmt.Fprintf(&sb, "- Agent: %s\n", agent)
	if conv.ProjectPath != "" {
		fmt.Fprintf(&sb, "- Project: `%s`\n", conv.ProjectPath)
	}
	fmt.Fprintf(&sb, "- Session: `%s`\n", conv.ID)
	if !conv.CreatedAt.IsZero() {
		fmt.Fprintf(&sb, "- Created: %s\n", stamp(conv.CreatedAt))
	}
	if !conv.UpdatedAt.IsZero() {
		fmt.Fprintf(&sb, "- Updated: %s\n", stamp(conv.UpdatedAt))
	}
	if shown < total {
		fmt.Fprintf(&sb, "- Turns: %d, %d shown\n", total, shown)
	} else {
		fmt.Fprintf(&sb, "- Turns: %d\n", total)
	}
	if b != Full {
		fmt.Fprintf(&sb, "\n> Excerpt from another agent's session. Full transcript: `another export %s --provider %s --format markdown -o -`\n", conv.ID, conv.Provider)
	}
	return sb.String()
}

func renderBody(turns []turn, kept []int) string {
	var sb strings.Builder
	prev := -1
	for _, i := range kept {
		if skipped := i - prev - 1; skipped > 0 {
			fmt.Fprintf(&sb, "\n*… %d %s omitted …*\n", skipped, plural(skipped, "turn", "turns"))
		}
		t := turns[i]
		role := "User"
		if t.role == model.RoleAssistant {
			role = "Assistant"
		}
		if t.when.IsZero() {
			fmt.Fprintf(&sb, "\n## %d · %s\n\n", t.index, role)
		} else {
			fmt.Fprintf(&sb, "\n## %d · %s · %s\n\n", t.index, role, stamp(t.when))
		}
		sb.WriteString(t.text)
		sb.WriteString("\n")
		prev = i
	}
	if skipped := len(turns) - prev - 1; skipped > 0 && len(kept) > 0 {
		fmt.Fprintf(&sb, "\n*… %d %s omitted …*\n", skipped, plural(skipped, "turn", "turns"))
	}
	return sb.String()
}

// clip keeps line breaks, unlike util.TruncateRunes: code and lists only read
// as what they are with their newlines.
func clip(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("\n\n*… %d more characters …*", len(r)-n)
}

func stamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func runeLen(s string) int { return len([]rune(s)) }
