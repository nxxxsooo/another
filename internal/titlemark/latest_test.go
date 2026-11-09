package titlemark

import (
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/titler"
)

func TestLatestReturnsNewestManagedTitleAndIgnoresOtherProviders(t *testing.T) {
	store := At(t.TempDir())
	old := titler.Mark{Title: "older", At: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)}
	newest := titler.Mark{Title: "1109｜Explore｜Conversation Test", At: old.At.Add(time.Hour)}
	saveMark(t, store, "doubao", "one", old)
	saveMark(t, store, "doubao", "two", newest)
	saveMark(t, store, "other", "three", titler.Mark{Title: "other", At: newest.At.Add(time.Hour)})

	latest, ok := store.Latest("doubao")
	if !ok || latest.Title != newest.Title || !latest.At.Equal(newest.At) {
		t.Fatalf("latest=%+v ok=%v", latest, ok)
	}
	if latest, ok := store.Latest("missing"); ok || latest.Title != "" {
		t.Fatalf("missing provider returned %+v", latest)
	}
}

func saveMark(t *testing.T, store Store, provider, session string, mark titler.Mark) {
	t.Helper()
	if err := store.Save(provider, session, mark); err != nil {
		t.Fatal(err)
	}
}
