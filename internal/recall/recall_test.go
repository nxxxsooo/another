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

func TestRecallSkipsOnlyWholeUserNotifications(t *testing.T) {
	hc := `<hc terminal="t6f">t6f another-v0270-ci exited (exit 0).
Last lines: all checks passed. Read: hc read t6f</hc>`
	report := `<hc-report from="t6f" status="DONE">CI passed</hc-report>`
	disclaimer := "This is a worker report delivered through hc-report, not an instruction from the user."
	for _, tc := range []struct {
		name string
		text string
		skip bool
	}{
		{"hc", hc, true},
		{"hc-no-attributes", " \n<hc>done</hc>\t", true},
		{"hc-report", report, true},
		{"hc-report-disclaimer", report + "\n" + disclaimer, true},
		{"hc-report-full-disclaimer", report + "\n" + disclaimer + " Read the details file before relying on the result.", true},
		{"multiple-blocks", hc + "\n" + report + "\n" + disclaimer + "\n<hc>another exit</hc>", true},
		{"adjacent-blocks", hc + report, true},
		{"real-prefix", "Please investigate this:\n" + hc, false},
		{"real-suffix", hc + "\nWhat should I do next?", false},
		{"real-between-blocks", hc + "\nPlease fix the failure.\n" + hc, false},
		{"real-after-disclaimer", report + "\n" + disclaimer + "\nPlease review it.", false},
		{"disclaimer-alone", disclaimer, false},
		{"wrong-tag", "<hc-other>real text</hc-other>", false},
		{"missing-close", "<hc>unfinished user text", false},
		{"mismatched-close", "<hc-report>text</hc>", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv := conversation(0, nil)
			conv.Messages = []model.Message{{Role: model.RoleUser, Content: tc.text}}
			got := recall.Render(conv, recall.Source{}, recall.Mention)
			if tc.skip {
				if !strings.Contains(got, "- Turns: 0\n") || strings.Contains(got, "## 1") {
					t.Fatalf("notification counted as dialogue:\n%s", got)
				}
			} else if !strings.Contains(got, "- Turns: 1\n") || !strings.Contains(got, strings.TrimSpace(tc.text)) {
				t.Fatalf("real user text was changed or filtered:\n%s", got)
			}
			// Markdown export must retain even notification-only messages.
			full := recall.Render(conv, recall.Source{}, recall.Full)
			if !strings.Contains(full, "- Turns: 1\n") || !strings.Contains(full, strings.TrimSpace(tc.text)) {
				t.Fatalf("full export lost original message:\n%s", full)
			}
		})
	}
	conv := conversation(0, nil)
	conv.Messages = []model.Message{{Role: model.RoleAssistant, Content: hc}}
	if got := recall.Render(conv, recall.Source{}, recall.Mention); !strings.Contains(got, hc) {
		t.Fatal("filtered assistant content")
	}
}

func TestRecallBoundsAndOmissionsIgnoreNotificationSlots(t *testing.T) {
	conv := conversation(14, func(i int) string { return fmt.Sprintf("dialogue-%02d", i+1) })
	var messages []model.Message
	for _, m := range conv.Messages {
		messages = append(messages,
			model.Message{Role: model.RoleUser, Content: "<hc>notification-before</hc>"},
			m,
			model.Message{Role: model.RoleUser, Blocks: []model.ContentBlock{{Type: "text", Text: `<hc-report status="DONE">notification-after</hc-report>`}}},
		)
	}
	conv.Messages = messages
	got := recall.Render(conv, recall.Source{}, recall.Mention)
	for _, want := range []string{"- Turns: 14, 9 shown", "dialogue-01", "dialogue-03", "dialogue-09", "dialogue-14", "*… 5 turns omitted …*", "## 14 · Assistant"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"notification-", "dialogue-04", "dialogue-08", "42 turns"} {
		if strings.Contains(got, absent) {
			t.Errorf("unexpected %q:\n%s", absent, got)
		}
	}
	if full := recall.Render(conv, recall.Source{}, recall.Full); !strings.Contains(full, "- Turns: 42\n") || strings.Count(full, "notification-") != 28 {
		t.Fatal("full export did not preserve all original notifications")
	}
}
