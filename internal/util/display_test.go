package util

import "testing"

// The real title that broke the browser: a Qwen session whose first message was
// a captured Nerd Font prompt. Every width table calls those glyphs one cell;
// the terminal draws them two, and the row escapes its pane.
const capturedPrompt = "\ue0b6\U000f0035 mingjian \ue0b0 ~/\U000f0219 /sync \ue0b0\ue0b0 \uf43a 02:03 \ue0b4 \uf432 codex Error loading"

func TestSanitizeDisplayDropsUnmeasurableRunes(t *testing.T) {
	got := SanitizeDisplay(capturedPrompt)
	want := "mingjian ~/ /sync 02:03 codex Error loading"
	if got != want {
		t.Fatalf("SanitizeDisplay = %q, want %q", got, want)
	}
	for _, r := range got {
		if r >= 0xE000 && r <= 0xF8FF || r >= 0xF0000 {
			t.Fatalf("a private-use rune survived: %q", r)
		}
	}
}

// Ordinary titles must come through untouched, including the CJK and the
// full-width punctuation the title contract itself produces.
func TestSanitizeDisplayKeepsRealTitles(t *testing.T) {
	for _, title := range []string{
		"0906｜修复｜标题栏路径与 Agent 名称格式",
		"Review messages with Yipeng for issues",
		"https://mp.weixin.qq.com/s/001Uuczr7OdhUZWMm0mNMA ingest",
		"重构 smart-quest/openspec/changes — 第二轮",
	} {
		if got := SanitizeDisplay(title); got != title {
			t.Errorf("SanitizeDisplay(%q) = %q", title, got)
		}
	}
}

// A newline in a title used to be replaced by hand at three call sites. It is
// whitespace like any other now, and a run of it collapses so a dropped glyph
// does not leave a hole where the icon was.
func TestSanitizeDisplayFlattensWhitespace(t *testing.T) {
	if got := SanitizeDisplay("  first\n\tsecond   third \n"); got != "first second third" {
		t.Fatalf("SanitizeDisplay = %q", got)
	}
}
