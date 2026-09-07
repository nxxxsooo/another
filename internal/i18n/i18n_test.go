package i18n_test

import (
	"testing"

	"github.com/nxxxsooo/another/internal/i18n"
)

func env(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

// A configuration written before this setting existed has no ui.language, and
// that absence must mean auto rather than an arbitrary language.
func TestNormalizeUnknownMeansAuto(t *testing.T) {
	for _, in := range []i18n.Lang{"", "auto", "  ", "klingon", "follow"} {
		if got := i18n.Normalize(in); got != i18n.LangAuto {
			t.Fatalf("Normalize(%q) = %q, want auto", in, got)
		}
	}
	for _, in := range []i18n.Lang{"ZH", "中文", "zh-Hans", "chinese"} {
		if got := i18n.Normalize(in); got != i18n.LangChinese {
			t.Fatalf("Normalize(%q) = %q, want zh", in, got)
		}
	}
	for _, in := range []i18n.Lang{"EN", "english", " en "} {
		if got := i18n.Normalize(in); got != i18n.LangEnglish {
			t.Fatalf("Normalize(%q) = %q, want en", in, got)
		}
	}
}

// The whole point of the default is that a Chinese desktop gets Chinese and
// everyone else gets English without anyone configuring anything.
func TestDetectReadsLocale(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		want i18n.Lang
	}{
		{"chinese lang", map[string]string{"LANG": "zh_CN.UTF-8"}, i18n.LangChinese},
		{"chinese hans", map[string]string{"LANG": "zh-Hans"}, i18n.LangChinese},
		{"bare zh", map[string]string{"LANG": "zh"}, i18n.LangChinese},
		{"english", map[string]string{"LANG": "en_US.UTF-8"}, i18n.LangEnglish},
		{"unset", map[string]string{}, i18n.LangEnglish},
		{"c locale", map[string]string{"LANG": "C"}, i18n.LangEnglish},
		{"unknown locale", map[string]string{"LANG": "de_DE.UTF-8"}, i18n.LangEnglish},
		// A language that merely contains "zh" is not Chinese.
		{"not a zh tag", map[string]string{"LANG": "en_US.UTF-8@zh"}, i18n.LangEnglish},
		{"lc_all wins", map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "zh_CN.UTF-8"}, i18n.LangEnglish},
		{"lc_messages over lang", map[string]string{"LC_MESSAGES": "zh_CN.UTF-8", "LANG": "en_US.UTF-8"}, i18n.LangChinese},
		{"empty var falls through", map[string]string{"LC_ALL": "", "LANG": "zh_CN.UTF-8"}, i18n.LangChinese},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := i18n.Detect(env(tc.vars)); got != tc.want {
				t.Fatalf("Detect(%v) = %q, want %q", tc.vars, got, tc.want)
			}
		})
	}
}

// An explicit choice is the reason the setting exists: it has to beat a
// locale that says otherwise.
func TestResolvePrefersExplicitChoice(t *testing.T) {
	chinese := env(map[string]string{"LANG": "zh_CN.UTF-8"})
	if got := i18n.Resolve(i18n.LangEnglish, chinese); got != i18n.LangEnglish {
		t.Fatalf("explicit English lost to a Chinese locale: %q", got)
	}
	english := env(map[string]string{"LANG": "en_US.UTF-8"})
	if got := i18n.Resolve(i18n.LangChinese, english); got != i18n.LangChinese {
		t.Fatalf("explicit Chinese lost to an English locale: %q", got)
	}
	if got := i18n.Resolve(i18n.LangAuto, chinese); got != i18n.LangChinese {
		t.Fatalf("auto ignored the locale: %q", got)
	}
	if got := i18n.Resolve("", chinese); got != i18n.LangChinese {
		t.Fatalf("an unset preference must behave like auto: %q", got)
	}
}

// Current never reports auto: callers render words, and auto is not a
// language you can write a sentence in.
func TestCurrentIsAlwaysConcrete(t *testing.T) {
	previous := i18n.SetCurrent(i18n.LangChinese)
	t.Cleanup(func() { i18n.SetCurrent(previous) })
	if got := i18n.Current(); got != i18n.LangChinese {
		t.Fatalf("Current() = %q after setting zh", got)
	}
	i18n.SetCurrent(i18n.LangEnglish)
	if got := i18n.Current(); got != i18n.LangEnglish {
		t.Fatalf("Current() = %q after setting en", got)
	}
	i18n.SetCurrent(i18n.LangAuto)
	if got := i18n.Current(); got == i18n.LangAuto {
		t.Fatal("Current() returned auto; it must resolve to a real language")
	}
}

// The labels are the one piece of text that must stay readable to someone who
// cannot read the current interface language.
func TestLabelsNameThemselves(t *testing.T) {
	if got := i18n.Label(i18n.LangChinese); got != "中文" {
		t.Fatalf("Chinese label = %q", got)
	}
	if got := i18n.Label(i18n.LangEnglish); got != "English" {
		t.Fatalf("English label = %q", got)
	}
	if got := i18n.Label(i18n.LangAuto); got != "Auto" {
		t.Fatalf("Auto label = %q", got)
	}
}
