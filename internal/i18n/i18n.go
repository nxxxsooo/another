// Package i18n decides which language another speaks to the person at the
// keyboard. It holds the policy only: the words themselves live with the
// screen that draws them.
//
// This is deliberately not the same setting as the title language in
// internal/titler. Both offer auto, but they resolve it against different
// evidence: an interface follows the environment its user configured, while a
// title follows the session it names. Merging them would make one of the two
// autos silently wrong.
package i18n

import (
	"os"
	"strings"
	"sync/atomic"
)

// Lang is a language preference. Only English and Chinese are translated;
// Auto picks one of them from the environment.
type Lang string

const (
	// LangAuto follows the locale the terminal was started with.
	LangAuto Lang = "auto"
	// LangEnglish is the fallback for every locale another cannot read as
	// Chinese, including an unset one.
	LangEnglish Lang = "en"
	// LangChinese is selected by a zh locale, or chosen explicitly.
	LangChinese Lang = "zh"
)

// Normalize maps stored and typed values onto the three supported ones.
// Anything unrecognized, including empty, means auto — a configuration file
// written by an older build has no ui.language at all.
func Normalize(l Lang) Lang {
	switch Lang(strings.ToLower(strings.TrimSpace(string(l)))) {
	case LangEnglish, "english", "eng":
		return LangEnglish
	case LangChinese, "chinese", "中文", "zh-cn", "zh_cn", "zh-hans":
		return LangChinese
	default:
		return LangAuto
	}
}

// Label names a language for a settings row. The names stay in the language
// they refer to: a person looking for their own language recognizes it faster
// than a translated noun, and the row has to be usable in the language the
// reader cannot read.
func Label(l Lang) string {
	switch Normalize(l) {
	case LangEnglish:
		return "English"
	case LangChinese:
		return "中文"
	default:
		return "Auto"
	}
}

// localeVars are read in POSIX precedence: LC_ALL overrides everything,
// LC_MESSAGES governs program text specifically, and LANG is the default for
// the rest. The first one set wins even if its value is not Chinese, because
// that is still an explicit statement about this terminal.
var localeVars = []string{"LC_ALL", "LC_MESSAGES", "LANG"}

// Detect resolves auto against a locale. lookup is os.Getenv in production and
// a map in tests. A locale another does not recognize resolves to English:
// most of the world does not read Chinese, and a wrong guess in that direction
// leaves a user with an interface they cannot navigate at all.
func Detect(lookup func(string) string) Lang {
	for _, name := range localeVars {
		value := strings.TrimSpace(lookup(name))
		if value == "" {
			continue
		}
		if isChineseLocale(value) {
			return LangChinese
		}
		return LangEnglish
	}
	return LangEnglish
}

// isChineseLocale matches the language subtag only. "zh_CN.UTF-8", "zh-Hans",
// and a bare "zh" are all Chinese; "zh" appearing later in the string, as in a
// path-like LANG value, is not.
func isChineseLocale(value string) bool {
	tag := value
	for _, cut := range []string{".", "@", ":"} {
		if i := strings.Index(tag, cut); i >= 0 {
			tag = tag[:i]
		}
	}
	tag = strings.ToLower(strings.TrimSpace(tag))
	return tag == "zh" || strings.HasPrefix(tag, "zh_") || strings.HasPrefix(tag, "zh-")
}

// Resolve turns a stored preference into the language actually rendered. An
// explicit choice is honored even when it contradicts the locale; that is the
// entire reason the setting exists.
func Resolve(pref Lang, lookup func(string) string) Lang {
	if l := Normalize(pref); l != LangAuto {
		return l
	}
	return Detect(lookup)
}

// current is the resolved language for this process. It is a package-level
// value because it is a single user-wide preference read from far apart —
// the browser, the setup screen, and the bridge turn a migration writes into
// Pi — and threading it through every one of those call sites would say
// nothing true that this does not.
var current atomic.Value

// Current is the language another is speaking right now. Before anything
// resolves a preference it is English, so a test or a command that never
// touches configuration still renders in a real language.
func Current() Lang {
	if l, ok := current.Load().(Lang); ok {
		return l
	}
	return LangEnglish
}

// SetCurrent records the resolved language and returns what it replaced, so a
// caller that changes it temporarily can put it back.
func SetCurrent(l Lang) Lang {
	previous := Current()
	resolved := Normalize(l)
	if resolved == LangAuto {
		resolved = Detect(os.Getenv)
	}
	current.Store(resolved)
	return previous
}
