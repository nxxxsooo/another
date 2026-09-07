package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/i18n"
	"github.com/nxxxsooo/another/internal/titler"
)

// useLanguage renders the rest of the test in one language and puts the
// previous one back. Tests that assert on words must say which language they
// are asserting in; the default is English, and a test that silently depended
// on that would break the moment the default changed.
func useLanguage(t *testing.T, lang i18n.Lang) {
	t.Helper()
	previous := applyLanguage(lang)
	t.Cleanup(func() { applyLanguage(previous) })
}

// A missing translation renders as an empty line rather than as an error, so
// the only place it can be caught is here.
func TestEveryStringIsTranslated(t *testing.T) {
	english := reflect.ValueOf(englishText)
	chinese := reflect.ValueOf(chineseText)
	typ := english.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		switch typ.Field(i).Type.Kind() {
		case reflect.String:
			if english.Field(i).String() == "" {
				t.Errorf("englishText.%s is empty", name)
			}
			if chinese.Field(i).String() == "" {
				t.Errorf("chineseText.%s is empty", name)
			}
		case reflect.Int:
			if english.Field(i).Int() <= 0 {
				t.Errorf("englishText.%s is not a usable width", name)
			}
			if chinese.Field(i).Int() <= 0 {
				t.Errorf("chineseText.%s is not a usable width", name)
			}
		}
	}
}

// A format string that loses a verb in translation prints "%!d(MISSING)" into
// the interface, which is worse than an untranslated line.
func TestFormatVerbsMatchAcrossLanguages(t *testing.T) {
	english := reflect.ValueOf(englishText)
	chinese := reflect.ValueOf(chineseText)
	typ := english.Type()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type.Kind() != reflect.String {
			continue
		}
		name := typ.Field(i).Name
		en, zh := english.Field(i).String(), chinese.Field(i).String()
		if got, want := strings.Count(zh, "%"), strings.Count(en, "%"); got != want {
			t.Errorf("%s: English has %d verbs, Chinese has %d", name, want, got)
		}
	}
}

// Every reason the batch engine can produce has to have words in both
// languages, or a frozen row explains itself with an identifier.
func TestEveryFreezeReasonHasWords(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		expected := map[titler.FreezeReason]string{
			titler.FreezeMissingCreatedAt:   txt.freezeMissingCreatedAt,
			titler.FreezeCancelled:          txt.freezeCancelled,
			titler.FreezeDuplicateTitle:     txt.freezeDuplicateTitle,
			titler.FreezeNotIndexed:         txt.freezeNotIndexed,
			titler.FreezeCurrentSession:     txt.freezeCurrentSession,
			titler.FreezeRenameUnsupported:  txt.freezeRenameUnsupported,
			titler.FreezeSuggestUnsupported: txt.freezeSuggestUnsupported,
		}
		for reason, want := range expected {
			if got := freezeText(reason); got != want {
				t.Errorf("%s: freeze reason %q renders as %q, want %q", lang, reason, got, want)
			}
		}
	}
	// A reason this table has never heard of still has to say something.
	if got := freezeText(titler.FreezeReason("invented")); got != "invented" {
		t.Errorf("unknown reason rendered as %q", got)
	}
}

// Every listing failure another can produce has to have words in both
// languages. The picker reports these verbatim, so an untranslated one is a
// Chinese sentence on an English screen.
func TestEveryListFailureHasWords(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		reasons := []titler.ListFailure{
			titler.ListUnsupported,
			titler.ListNoTitles,
			titler.ListNotInstalled,
			titler.ListTimedOut,
			titler.ListEmpty,
			titler.ListFailed,
		}
		for _, reason := range reasons {
			err := &titler.ListError{Reason: reason, Command: "pi", Detail: "exit status 1"}
			got := listErrorText(err)
			if got == "" || !strings.Contains(got, "pi") {
				t.Errorf("%s: %q rendered as %q", lang, reason, got)
			}
			if got == err.Error() && lang == i18n.LangChinese {
				t.Errorf("%s: %q was not translated: %q", lang, reason, got)
			}
		}
	}
	// Anything that is not another's own failure keeps the words it came
	// with; a CLI's message is the CLI's to word.
	useLanguage(t, i18n.LangChinese)
	if got := listErrorText(errors.New("some other failure")); got != "some other failure" {
		t.Errorf("a foreign error was rewritten as %q", got)
	}
}

// The suggestion errors are shown under the rename box and beside a failed
// batch row, so they need words in both languages too.
func TestEverySuggestFailureHasWords(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		reasons := []titler.SuggestFailure{
			titler.SuggestNoCreatedAt,
			titler.SuggestNoTitles,
			titler.SuggestNotInstalled,
			titler.SuggestTimedOut,
			titler.SuggestFailed,
		}
		for _, reason := range reasons {
			err := &titler.SuggestError{Reason: reason, Command: "pi", Detail: "exit status 1"}
			if got := suggestErrorText(err); got == "" {
				t.Errorf("%s: %q rendered as nothing", lang, reason)
			} else if lang == i18n.LangChinese && got == err.Error() {
				t.Errorf("%s: %q was not translated: %q", lang, reason, got)
			}
		}
	}
	useLanguage(t, i18n.LangChinese)
	if got := suggestErrorText(errors.New("some other failure")); got != "some other failure" {
		t.Errorf("a foreign error was rewritten as %q", got)
	}
}

// The interface language is resolved from configuration, and an unset
// preference has to reach a real catalog rather than a nil one.
func TestSetLanguageResolvesEveryPreference(t *testing.T) {
	previous := i18n.Current()
	t.Cleanup(func() { applyLanguage(previous) })

	SetLanguage("zh")
	if txt != &chineseText {
		t.Fatal("zh did not select the Chinese catalog")
	}
	SetLanguage("en")
	if txt != &englishText {
		t.Fatal("en did not select the English catalog")
	}
	t.Setenv("LC_ALL", "zh_CN.UTF-8")
	SetLanguage("")
	if txt != &chineseText {
		t.Fatal("an unset preference ignored a Chinese locale")
	}
	t.Setenv("LC_ALL", "en_US.UTF-8")
	SetLanguage("auto")
	if txt != &englishText {
		t.Fatal("auto ignored an English locale")
	}
}
