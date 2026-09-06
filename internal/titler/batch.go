package titler

import (
	"context"
	"sync"
	"time"
)

// DefaultConcurrency bounds how many agent CLIs run at once. Each suggestion
// is a full CLI process and a model round trip, not a cheap API call: measured
// cold-start latency ranges from about 6s (pi) to about 18s (agy), so a large
// batch is unusable when serialized, and unfriendly to the machine when not
// bounded at all.
const DefaultConcurrency = 4

// maxAttempts bounds how many times one row may call a model. Agent CLIs fail
// transiently often enough to matter over dozens of rows — a cold start that
// runs past the timeout, a rate limit, a process that dies once — and a second
// attempt is cheap next to making the person re-run the whole batch. It stays
// at two so a systematically broken setup fails fast instead of burning
// minutes per row.
const maxAttempts = 2

// retryDelay spaces the retry. Immediate retries are the wrong answer to the
// most common transient failure here, which is an agent that is busy or rate
// limited. It is a variable so tests do not have to wait it out.
var retryDelay = 2 * time.Second

// BatchItem pairs a session identity with the request that names it.
type BatchItem struct {
	SessionID string
	Request   Request
}

// BatchResult reports one row. Exactly one of Title, Frozen, or Err carries the
// outcome: a frozen row was never sent to a model, an empty Title with no error
// means the model declined, and Err means the attempt failed.
type BatchResult struct {
	SessionID string
	Current   string
	Title     string
	Frozen    string
	Err       error
}

// Changed reports whether this row would actually rename anything.
func (r BatchResult) Changed() bool {
	return r.Err == nil && r.Frozen == "" && r.Title != "" && r.Title != r.Current
}

// suggestFunc is the seam that keeps batch tests off real agent CLIs.
type suggestFunc func(context.Context, Config, Request) (string, error)

// SuggestBatch runs the configured agent over every item and streams results as
// they land. The channel closes when every item is accounted for, including on
// cancellation, so a caller can always drain it to completion.
//
// Results arrive out of order. Callers that need batch-wide rules must collect
// first and then apply FreezeDuplicates.
func SuggestBatch(ctx context.Context, cfg Config, items []BatchItem, workers int) <-chan BatchResult {
	return suggestBatch(ctx, cfg, items, workers, Suggest)
}

func suggestBatch(ctx context.Context, cfg Config, items []BatchItem, workers int, suggest suggestFunc) <-chan BatchResult {
	if workers < 1 {
		workers = DefaultConcurrency
	}
	if workers > len(items) {
		workers = len(items)
	}
	out := make(chan BatchResult)
	if len(items) == 0 {
		close(out)
		return out
	}

	queue := make(chan BatchItem)
	go func() {
		defer close(queue)
		for _, item := range items {
			select {
			case queue <- item:
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for item := range queue {
				res := runOne(ctx, cfg, item, suggest)
				select {
				case out <- res:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func runOne(ctx context.Context, cfg Config, item BatchItem, suggest suggestFunc) BatchResult {
	res := BatchResult{SessionID: item.SessionID, Current: item.Request.Title}
	// A row whose creation date cannot be proven is frozen here rather than
	// sent to a model, which keeps another from paying for a call whose only
	// possible answers are a refusal or an invented date. Unix 0 counts as
	// missing too: that is how the index stores it, and it scans back as 1970.
	if item.Request.CreatedAt.Unix() <= 0 {
		res.Frozen = "缺少创建时间"
		return res
	}
	if ctx.Err() != nil {
		res.Frozen = "已取消"
		return res
	}
	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 && !wait(ctx, retryDelay) {
			break
		}
		title, err := suggest(ctx, cfg, item.Request)
		if err == nil {
			res.Title = title
			return res
		}
		last = err
		// A permanent failure and a cancelled batch both mean the next
		// attempt cannot do better than this one.
		if Permanent(err) || ctx.Err() != nil {
			break
		}
	}
	res.Err = last
	return res
}

// wait sleeps unless the batch is cancelled first.
func wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// FreezeDuplicates freezes every row that proposes a title another row in the
// same batch also proposes. Two sessions renamed to one string are worse than
// two unchanged sessions: the batch would silently make them indistinguishable
// in the list it was meant to tidy.
//
// It returns a new slice and does not reorder the input.
func FreezeDuplicates(results []BatchResult) []BatchResult {
	counts := make(map[string]int, len(results))
	for _, r := range results {
		if r.Changed() {
			counts[r.Title]++
		}
	}
	out := make([]BatchResult, len(results))
	copy(out, results)
	for i, r := range out {
		if r.Changed() && counts[r.Title] > 1 {
			out[i].Frozen = "批内标题重复"
			out[i].Title = ""
		}
	}
	return out
}

// BatchCounts summarizes a finished batch for the confirmation line.
type BatchCounts struct{ Changed, Unchanged, Frozen, Failed int }

// Summarize counts rows by outcome.
func Summarize(results []BatchResult) BatchCounts {
	var c BatchCounts
	for _, r := range results {
		switch {
		case r.Err != nil:
			c.Failed++
		case r.Frozen != "":
			c.Frozen++
		case r.Changed():
			c.Changed++
		default:
			c.Unchanged++
		}
	}
	return c
}
