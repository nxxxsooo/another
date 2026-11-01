package titler_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/titler"
)

// shanghaiDay is 10:00 in Shanghai on the given September day.
func shanghaiDay(day int) time.Time {
	return time.Date(2026, 9, day, 2, 0, 0, 0, time.UTC)
}

func TestDecideRefreshesOnlyTitlesAnotherOwns(t *testing.T) {
	owned := "0901｜修复｜Stripe重试"
	mark := &titler.Mark{Title: owned, UserTurns: 2}
	cases := []struct {
		name   string
		in     titler.Freshness
		plan   titler.Plan
		newDay bool
	}{
		{"untitled is named", titler.Freshness{Activity: shanghaiDay(1)}, titler.PlanName, false},
		{"same day, no new turns", titler.Freshness{Title: owned, Mark: mark, UserTurns: 2, Activity: shanghaiDay(1)}, titler.PlanSkip, false},
		{"two more turns is not yet drift", titler.Freshness{Title: owned, Mark: mark, UserTurns: 4, Activity: shanghaiDay(1)}, titler.PlanSkip, false},
		{"three more turns is checked", titler.Freshness{Title: owned, Mark: mark, UserTurns: 5, Activity: shanghaiDay(1)}, titler.PlanRefresh, false},
		{"a later day is checked and redated", titler.Freshness{Title: owned, Mark: mark, UserTurns: 3, Activity: shanghaiDay(2)}, titler.PlanRefresh, true},
		{"shrunk history is recounted", titler.Freshness{Title: owned, Mark: mark, UserTurns: 1, Activity: shanghaiDay(1)}, titler.PlanAdopt, false},
		{"a policy title from before marks is adopted", titler.Freshness{Title: owned, UserTurns: 9, Activity: shanghaiDay(1)}, titler.PlanAdopt, false},
		{"a policy title from before marks still follows a new day", titler.Freshness{Title: owned, UserTurns: 9, Activity: shanghaiDay(3)}, titler.PlanRefresh, true},
		// The person's title follows the policy too: only the mark tells it apart.
		{"a policy-shaped rename by hand is kept", titler.Freshness{Title: "0901｜功能｜我自己起的", Mark: mark, UserTurns: 20, Activity: shanghaiDay(5)}, titler.PlanSkip, false},
		{"a free-form rename by hand is kept", titler.Freshness{Title: "my own name", Mark: mark, UserTurns: 20, Activity: shanghaiDay(5), ReplaceNative: true}, titler.PlanSkip, false},
		{"an untouched agent title is kept by default", titler.Freshness{Title: "Fix stripe retries", Activity: shanghaiDay(1)}, titler.PlanSkip, false},
		{"an untouched agent title is replaced when the agent names itself", titler.Freshness{Title: "Fix stripe retries", Activity: shanghaiDay(1), ReplaceNative: true}, titler.PlanName, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := titler.Decide(tc.in)
			if got.Plan != tc.plan || got.NewDay != tc.newDay {
				t.Fatalf("Decide = %+v, want plan %s newDay %v", got, tc.plan, tc.newDay)
			}
		})
	}
}

// The date belongs to another, never to the model: a refreshed title carries
// the Shanghai date of the latest activity whatever the answer said.
func TestRedateKeepsTheTopicAndMovesTheDate(t *testing.T) {
	late := time.Date(2026, 9, 1, 17, 30, 0, 0, time.UTC) // 01:30 on 09-02 in Shanghai
	if got := titler.Redate("0901｜修复｜Stripe重试", late); got != "0902｜修复｜Stripe重试" {
		t.Fatalf("Redate = %q", got)
	}
	if got := titler.Redate("my own name", late); got != "my own name" {
		t.Fatalf("Redate touched a non-policy title: %q", got)
	}
	if got := titler.Topic("0902｜Fix｜Stripe retries"); got != "Fix｜Stripe retries" {
		t.Fatalf("Topic = %q", got)
	}
	if got := titler.TitleDay("09a1｜Fix｜x"); got != "" {
		t.Fatalf("TitleDay accepted a non-date: %q", got)
	}
}

func TestUserTurnsCountsOnlyWhatAPersonSaid(t *testing.T) {
	msgs := []model.Message{
		{Role: model.RoleUser, Content: "one"},
		{Role: model.RoleAssistant, Content: "reply"},
		{Role: model.RoleTool, Content: "output"},
		{Role: model.RoleUser, Content: "  "},
		{Role: model.RoleUser, Content: "two"},
	}
	if got := titler.UserTurns(msgs); got != 2 {
		t.Fatalf("UserTurns = %d, want 2", got)
	}
}

// A refresh asks about the session as it is now: the ending dominates, the
// date is the activity date, and repeating the current title is how the
// model keeps it.
func TestRefreshPromptWeightsTheEnding(t *testing.T) {
	var msgs []model.Message
	for i := 1; i <= 12; i++ {
		msgs = append(msgs, model.Message{Role: model.RoleUser, Content: "turn " + string(rune('A'+i))})
	}
	prompt := titler.BuildPrompt(titler.Request{
		Title: "0901｜修复｜Stripe重试", CreatedAt: shanghaiDay(1), Date: shanghaiDay(2),
		Messages: msgs, Refresh: true,
	}, titler.LangChinese)
	for _, want := range []string{
		"MMDD is fixed to 0902",
		"repeat its 类型 and 主题 unchanged",
		"Current title: 0901｜修复｜Stripe重试",
		"turn B", "turn C", // the opening keeps its intent
		"turn H", "turn M", // the last six carry the rest
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("refresh prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "turn D") {
		t.Error("refresh prompt kept the middle of the session over its ending")
	}

	first := titler.BuildPrompt(titler.Request{CreatedAt: shanghaiDay(1), Messages: msgs}, titler.LangChinese)
	if strings.Contains(first, "unchanged") || !strings.Contains(first, "MMDD is fixed to 0901") {
		t.Error("a first title must keep the creation date and the naming prompt")
	}
}
