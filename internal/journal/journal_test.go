package journal

import (
	"testing"
	"time"
)

func TestRecordAndReadBack(t *testing.T) {
	s := At(t.TempDir())
	at := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	entries := []Entry{
		{ID: NewID(at), At: at, Action: ActionRename, Provider: "pi", Session: "s1", Before: "old", After: "new", Revertible: true},
		{ID: NewID(at.Add(time.Second)), At: at.Add(time.Second), Action: ActionArchive, Provider: "pi", Session: "s2", Archived: true, Revertible: true},
	}
	for _, e := range entries {
		if err := s.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	if got[0].Action != ActionRename || got[0].Before != "old" || got[0].After != "new" {
		t.Fatalf("first entry = %+v", got[0])
	}
	if got[1].Action != ActionArchive || !got[1].Archived {
		t.Fatalf("second entry = %+v", got[1])
	}
}

func TestSnapshotRoundTripsAndDrops(t *testing.T) {
	s := At(t.TempDir())
	entry := Entry{ID: NewID(time.Now()), At: time.Now(), Action: ActionDelete, Provider: "claude-code", Session: "s1", Revertible: true}
	if err := s.Record(entry); err != nil {
		t.Fatal(err)
	}
	if s.Recovery(entry) {
		t.Fatal("a delete with no captured bytes must not read as recoverable")
	}
	want := []byte(`{"session":"bytes"}`)
	if err := s.SaveSnapshot(entry.ID, want); err != nil {
		t.Fatal(err)
	}
	if !s.Recovery(entry) {
		t.Fatal("a captured delete must read as recoverable")
	}
	got, ok := s.LoadSnapshot(entry.ID)
	if !ok || string(got) != string(want) {
		t.Fatalf("loaded snapshot = %q ok=%v", got, ok)
	}
	s.DropSnapshot(entry.ID)
	if _, ok := s.LoadSnapshot(entry.ID); ok {
		t.Fatal("a dropped snapshot must be gone")
	}
}

func TestSnapshotsAreBoundedByCount(t *testing.T) {
	s := At(t.TempDir())
	s.limits = Limits{MaxSnapshots: 2, MaxAge: time.Hour, MaxEntries: 100}
	var ids []string
	for i := 0; i < 4; i++ {
		e := Entry{ID: NewID(time.Now()), At: time.Now(), Action: ActionDelete, Provider: "pi", Session: "s", Revertible: true}
		if err := s.Record(e); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveSnapshot(e.ID, []byte("x")); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
		time.Sleep(2 * time.Millisecond)
	}
	// The two OLDEST must be pruned; the newest two kept.
	if _, ok := s.LoadSnapshot(ids[0]); ok {
		t.Fatal("the oldest snapshot survived the count limit")
	}
	if _, ok := s.LoadSnapshot(ids[1]); ok {
		t.Fatal("second-oldest snapshot survived the count limit")
	}
	for _, id := range ids[2:] {
		if _, ok := s.LoadSnapshot(id); !ok {
			t.Fatalf("recent snapshot %s was pruned", id)
		}
	}
}

func TestSnapshotsAreBoundedByAge(t *testing.T) {
	s := At(t.TempDir())
	s.limits = Limits{MaxSnapshots: 50, MaxAge: time.Hour, MaxEntries: 100}
	old := Entry{ID: NewID(time.Now()), At: time.Now(), Action: ActionDelete, Provider: "pi", Session: "s", Revertible: true}
	if err := s.Record(old); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot(old.ID, []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Age the snapshot past the window, then trigger a prune with a new record.
	index, _ := s.snapshotIndex()
	snap := index[old.ID]
	snap.At = time.Now().Add(-2 * time.Hour)
	index[old.ID] = snap
	if err := s.writeSnapshotIndex(index); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(Entry{ID: NewID(time.Now()), At: time.Now(), Action: ActionArchive, Provider: "pi", Session: "s2", Revertible: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadSnapshot(old.ID); ok {
		t.Fatal("a snapshot older than the age window survived")
	}
}

func TestLogIsCapped(t *testing.T) {
	s := At(t.TempDir())
	s.limits = Limits{MaxSnapshots: 50, MaxAge: time.Hour, MaxEntries: 3}
	for i := 0; i < 6; i++ {
		if err := s.Record(Entry{ID: NewID(time.Now()), At: time.Now(), Action: ActionArchive,
			Provider: "pi", Session: string(rune('a' + i)), Archived: true, Revertible: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	got, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	if got[0].Session != "d" {
		t.Fatalf("oldest kept = %q, want d (the newest three)", got[0].Session)
	}
}

func TestNewIDSortsByTime(t *testing.T) {
	early := NewID(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	late := NewID(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if early >= late {
		t.Fatalf("ids do not sort by time: %q >= %q", early, late)
	}
}
