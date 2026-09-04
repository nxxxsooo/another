package titler_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/titler"
)

func TestLiveSuggest(t *testing.T) {
	provider := os.Getenv("LIVE_PROVIDER")
	if provider == "" {
		t.Skip("set LIVE_PROVIDER=claude|codex|pi to call a real agent CLI")
	}
	start := time.Now()
	got, err := titler.Suggest(context.Background(), titler.Config{Provider: provider, Model: os.Getenv("LIVE_MODEL")}, titler.Request{
		Title:       "另一个会话",
		ProjectPath: "/Users/x/GitHub/another",
		CreatedAt:   time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC),
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "ctrl+d 删除会话时，删除快捷键和列表里的向下翻页冲突了，按一下会连着触发两次"},
			{Role: model.RoleAssistant, Content: "我看了 tui.go 的按键分发，删除确认弹窗没有吞掉按键，所以同一个 KeyMsg 又落到了列表上。修复方式是在 overlay 分支里提前 return。"},
			{Role: model.RoleUser, Content: "对，就这么改，然后补个测试"},
		},
	})
	t.Logf("provider=%s took=%s title=%q err=%v", provider, time.Since(start).Round(time.Second), got, err)
	if err != nil {
		t.Fatalf("live suggest failed: %v", err)
	}
	if got == "" {
		t.Fatal("live run produced no usable suggestion")
	}
}
