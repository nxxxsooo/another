package titler_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/titler"
)

func TestRequiredTitlePromptNamesSparseOpeningsWithoutInventingWork(t *testing.T) {
	for _, tc := range []struct {
		lang                        titler.Language
		kind, testTopic, emptyTopic string
	}{
		{titler.LangEnglish, "Explore", "Conversation Test", "New Conversation"},
		{titler.LangChinese, "探索", "会话测试", "新会话"},
		{titler.LangAuto, "Explore", "Conversation Test", "New Conversation"},
	} {
		t.Run(string(tc.lang), func(t *testing.T) {
			req := titler.Request{CreatedAt: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), RequireTitle: true,
				Messages: []model.Message{{Role: model.RoleUser, Content: "test"}, {Role: model.RoleAssistant, Content: "We could implement a payment dashboard."}}}
			prompt := titler.BuildPrompt(req, tc.lang)
			for _, want := range []string{"MMDD is fixed to 1109", "Do not reply KEEP solely", "use exactly type " + tc.kind, tc.testTopic, tc.emptyTopic, "checks the connection", "Never invent concrete project work", "merely suggested by the assistant"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q", want)
				}
			}
			if strings.Contains(prompt, "reply with exactly KEEP") {
				t.Fatal("required naming still instructs the model to decline sparse input")
			}
		})
	}
}

func TestOptionalTitlePromptStillAllowsKeep(t *testing.T) {
	req := titler.Request{CreatedAt: time.Now(), Messages: []model.Message{{Role: model.RoleUser, Content: "test"}}}
	for _, refresh := range []bool{false, true} {
		req.Refresh = refresh
		prompt := titler.BuildPrompt(req, titler.LangEnglish)
		if !strings.Contains(prompt, "reply with exactly KEEP") || strings.Contains(prompt, "Conversation Test") || strings.Contains(prompt, "A title is required") {
			t.Fatal("optional naming contract changed")
		}
	}
	// Required naming changes the request, not output acceptance: KEEP and bad
	// output must not be silently replaced with a fabricated title.
	for _, raw := range []string{"KEEP", "1109｜InventedType｜Conversation Test", "arbitrary malformed output"} {
		if got := titler.Clean(raw, titler.LangEnglish); got != "" {
			t.Fatalf("Clean(%q)=%q", raw, got)
		}
	}
}
