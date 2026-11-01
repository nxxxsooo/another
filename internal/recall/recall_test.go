package recall_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/recall"
)

var start = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func conversation(turns int, text func(i int) string) *model.Conversation {
	conv := &model.Conversation{
		ID: "sess-1", Provider: "claude-code", ProjectPath: "/work/repo", Title: "0901｜Fix｜Webhook retries",
		CreatedAt: start, UpdatedAt: start.Add(time.Duration(turns) * time.Minute),
	}
	for i := 0; i < turns; i++ {
		role := model.RoleUser
		if i%2 == 1 {
			role = model.RoleAssistant
		}
		conv.Messages = append(conv.Messages, model.Message{Role: role, Content: text(i), Timestamp: start.Add(time.Duration(i) * time.Minute)})
	}
	return conv
}

func TestShortSessionRendersWhole(t *testing.T) {
	conv := conversation(2, func(i int) string { return fmt.Sprintf("line one %d\nline two %d", i, i) })
	conv.Messages = append(conv.Messages,
		model.Message{Role: model.RoleTool, Content: "tool output"},
		model.Message{Role: model.RoleAssistant, Content: "   "},
	)
	got := recall.Render(conv, recall.Source{AgentName: "Claude Code"}, recall.Mention)
	want := "# 0901｜Fix｜Webhook retries\n\n" +
		"- Agent: Claude Code (`claude-code`)\n" +
		"- Project: `/work/repo`\n" +
		"- Session: `sess-1`\n" +
		"- Created: 2026-09-01 09:00 UTC\n" +
		"- Updated: 2026-09-01 09:02 UTC\n" +
		"- Turns: 2\n" +
		"\n> Excerpt from another agent's session. Full transcript: `another export sess-1 --provider claude-code --format markdown -o -`\n" +
		"\n## 1 · User · 2026-09-01 09:00 UTC\n\nline one 0\nline two 0\n" +
		"\n## 2 · Assistant · 2026-09-01 09:01 UTC\n\nline one 1\nline two 1\n"
	if got != want {
		t.Fatalf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestLongSessionKeepsOpeningAndClosingTurns(t *testing.T) {
	conv := conversation(200, func(i int) string { return fmt.Sprintf("turn-%03d", i+1) })
	got := recall.Render(conv, recall.Source{}, recall.Mention)
	for _, want := range []string{"turn-001", "turn-002", "turn-003", "turn-195", "turn-200", "*… 191 turns omitted …*", "- Turns: 200, 9 shown"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, absent := range []string{"turn-004", "turn-194"} {
		if strings.Contains(got, absent+"\n") {
			t.Errorf("%q should be omitted", absent)
		}
	}
}

func TestMentionStaysWithinTotalAndKeepsTheEnds(t *testing.T) {
	conv := conversation(200, func(i int) string {
		return fmt.Sprintf("turn-%03d\n", i+1) + strings.Repeat("ж", 5000)
	})
	got := recall.Render(conv, recall.Source{}, recall.Mention)
	if n := len([]rune(got)); n > recall.Mention.Total {
		t.Fatalf("rendered %d runes, cap %d", n, recall.Mention.Total)
	}
	for _, want := range []string{"turn-001", "turn-200", "more characters …*"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}

	tight := recall.Bounds{Head: 3, Tail: 6, PerMessage: 2000, Total: 5000}
	got = recall.Render(conv, recall.Source{}, tight)
	if n := len([]rune(got)); n > tight.Total {
		t.Fatalf("tight render %d runes, cap %d", n, tight.Total)
	}
	if !strings.Contains(got, "turn-001") || !strings.Contains(got, "turn-200") {
		t.Fatalf("tight render lost the first or last turn:\n%s", got[:200])
	}
}

func TestFullKeepsEveryTurnUnclipped(t *testing.T) {
	long := strings.Repeat("x", 9000)
	conv := conversation(40, func(i int) string { return fmt.Sprintf("turn-%03d %s", i+1, long) })
	got := recall.Render(conv, recall.Source{}, recall.Full)
	if strings.Contains(got, "omitted") || strings.Contains(got, "more characters") || strings.Contains(got, "Excerpt") {
		t.Fatal("full render must not elide anything")
	}
	if strings.Count(got, long) != 40 {
		t.Fatalf("full render kept %d of 40 turns", strings.Count(got, long))
	}
}
