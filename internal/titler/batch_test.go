package titler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func item(id, title string, created time.Time) BatchItem {
	return BatchItem{SessionID: id, Request: Request{Title: title, CreatedAt: created}}
}

func drain(ch <-chan BatchResult) []BatchResult {
	var got []BatchResult
	for r := range ch {
		got = append(got, r)
	}
	return got
}

func byID(results []BatchResult) map[string]BatchResult {
	m := make(map[string]BatchResult, len(results))
	for _, r := range results {
		m[r.SessionID] = r
	}
	return m
}

var created = time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)

func TestBatchNeverExceedsItsWorkerBound(t *testing.T) {
	const workers = 4
	var live, peak int64
	suggest := func(ctx context.Context, _ Config, _ Request) (string, error) {
		n := atomic.AddInt64(&live, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if n <= old || atomic.CompareAndSwapInt64(&peak, old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&live, -1)
		return "0903｜功能｜某个主题", nil
	}

	items := make([]BatchItem, 40)
	for i := range items {
		items[i] = item(fmt.Sprintf("s%d", i), "old", created)
	}
	got := drain(suggestBatch(context.Background(), Config{Provider: "pi"}, items, workers, suggest))

	if len(got) != len(items) {
		t.Fatalf("lost rows: got %d of %d", len(got), len(items))
	}
	if peak > workers {
		t.Fatalf("ran %d agent CLIs at once, bound is %d", peak, workers)
	}
	if peak < 2 {
		t.Fatalf("never ran concurrently at all: peak=%d", peak)
	}
}

func TestBatchFreezesRowsWithoutACreationTime(t *testing.T) {
	var called int64
	suggest := func(context.Context, Config, Request) (string, error) {
		atomic.AddInt64(&called, 1)
		return "0903｜功能｜某个主题", nil
	}
	items := []BatchItem{
		item("dated", "old", created),
		item("undated", "old", time.Time{}),
		item("epoch", "old", time.Unix(0, 0)),
	}
	got := byID(drain(suggestBatch(context.Background(), Config{}, items, 2, suggest)))

	if got["undated"].Frozen != "缺少创建时间" {
		t.Fatalf("undated row was not frozen: %+v", got["undated"])
	}
	// Unix 0 is how the index stores a missing creation time; it scans back
	// as 1970, so the engine must treat it as missing rather than handing the
	// model an invented MMDD of 0101.
	if got["epoch"].Frozen != "缺少创建时间" {
		t.Fatalf("epoch-zero row was not frozen: %+v", got["epoch"])
	}
	if got["undated"].Title != "" {
		t.Fatal("a frozen row must not carry a title")
	}
	if called != 1 {
		t.Fatalf("a frozen row must never reach a model: %d calls", called)
	}
}

func TestOneFailureLeavesTheRestOfTheBatchIntact(t *testing.T) {
	suggest := func(_ context.Context, _ Config, req Request) (string, error) {
		if req.Title == "boom" {
			return "", errors.New("pi 生成超时")
		}
		return "0903｜功能｜" + req.Title, nil
	}
	items := []BatchItem{
		item("ok1", "alpha", created),
		item("bad", "boom", created),
		item("ok2", "beta", created),
	}
	got := byID(drain(suggestBatch(context.Background(), Config{}, items, 3, suggest)))

	if got["bad"].Err == nil {
		t.Fatal("the failing row reported no error")
	}
	if !got["ok1"].Changed() || !got["ok2"].Changed() {
		t.Fatalf("one failure took healthy rows with it: %+v %+v", got["ok1"], got["ok2"])
	}
	if c := Summarize([]BatchResult{got["ok1"], got["bad"], got["ok2"]}); c.Changed != 2 || c.Failed != 1 {
		t.Fatalf("unexpected counts: %+v", c)
	}
}

func TestCancellationStopsTheBatchAndStillClosesTheChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started sync.Once
	var calls int64
	suggest := func(ctx context.Context, _ Config, _ Request) (string, error) {
		atomic.AddInt64(&calls, 1)
		started.Do(cancel)
		<-ctx.Done()
		return "", ctx.Err()
	}

	items := make([]BatchItem, 50)
	for i := range items {
		items[i] = item(fmt.Sprintf("s%d", i), "old", created)
	}

	done := make(chan []BatchResult)
	go func() { done <- drain(suggestBatch(ctx, Config{}, items, 4, suggest)) }()

	select {
	case got := <-done:
		if len(got) >= len(items) {
			t.Fatalf("cancellation did not stop the batch: %d of %d ran", len(got), len(items))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the result channel never closed after cancellation")
	}
}

func TestDuplicateProposalsFreezeEveryCollidingRow(t *testing.T) {
	results := []BatchResult{
		{SessionID: "a", Current: "old a", Title: "0903｜修复｜同一个主题"},
		{SessionID: "b", Current: "old b", Title: "0903｜修复｜同一个主题"},
		{SessionID: "c", Current: "old c", Title: "0903｜功能｜独有主题"},
	}
	got := byID(FreezeDuplicates(results))

	for _, id := range []string{"a", "b"} {
		if got[id].Frozen != "批内标题重复" {
			t.Fatalf("colliding row %s was not frozen: %+v", id, got[id])
		}
		if got[id].Title != "" {
			t.Fatalf("frozen row %s kept its title", id)
		}
	}
	if !got["c"].Changed() {
		t.Fatalf("the unique row was frozen too: %+v", got["c"])
	}
	if results[0].Frozen != "" {
		t.Fatal("FreezeDuplicates mutated its input")
	}
}

func TestARowThatKeepsItsTitleIsNotAChange(t *testing.T) {
	same := BatchResult{SessionID: "a", Current: "0903｜修复｜主题", Title: "0903｜修复｜主题"}
	declined := BatchResult{SessionID: "b", Current: "old", Title: ""}
	if same.Changed() || declined.Changed() {
		t.Fatalf("unchanged rows reported as changes: %+v %+v", same, declined)
	}
	if c := Summarize([]BatchResult{same, declined}); c.Unchanged != 2 || c.Changed != 0 {
		t.Fatalf("unexpected counts: %+v", c)
	}
}
