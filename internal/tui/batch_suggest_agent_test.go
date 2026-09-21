package tui

import (
	"context"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
)

func batchRow() model.Summary {
	return model.Summary{ID: "s1", Provider: "claude-code", Title: "old", CreatedAt: time.Now()}
}

func prepared(t *testing.T, cfg titler.Config, reg *registry.Registry) batchReadyMsg {
	t.Helper()
	msg := batchPrepareCmd(context.Background(), 1, reg, cfg, []model.Summary{batchRow()}, nil)()
	ready, ok := msg.(batchReadyMsg)
	if !ok {
		t.Fatalf("prepare returned %T, want batchReadyMsg", msg)
	}
	return ready
}

// The suggesting agent is the configured one. A session from an agent that
// cannot itself write titles is still nameable, so the freeze must follow the
// configuration rather than the row.
func TestBatchFreezesOnTheConfiguredAgentNotTheSessionsOwn(t *testing.T) {
	// "cursor" has no titler launcher. As the configured agent that is fatal
	// for the whole batch; as a session's own provider it is irrelevant.
	ready := prepared(t, titler.Config{Provider: "cursor"}, registry.New())
	if len(ready.items) != 0 {
		t.Fatalf("items = %+v, want none when the configured agent cannot suggest", ready.items)
	}
	if len(ready.frozen) != 1 || ready.frozen[0].Frozen != titler.FreezeSuggestUnsupported {
		t.Fatalf("frozen = %+v, want one suggest-unsupported row", ready.frozen)
	}
}

func TestBatchAcceptsAConfiguredAgentThatCanSuggest(t *testing.T) {
	ready := prepared(t, titler.Config{Provider: "agy"}, registry.New())
	for _, r := range ready.frozen {
		if r.Frozen == titler.FreezeSuggestUnsupported {
			t.Fatalf("row frozen as suggest-unsupported under a valid agent: %+v", r)
		}
	}
}

// A desktop source can own native rename without offering a headless model.
// Its rows still reach the configured title agent through the normal flow.
type desktopRenamer struct{ fakeRenamer }

func (*desktopRenamer) ID() string { return "doubao" }

func TestBatchPreparesRenamableSourceWithoutTitlerLauncher(t *testing.T) {
	p := &desktopRenamer{}
	if titler.Supports(p.ID()) {
		t.Fatal("test needs a source without a title launcher")
	}
	sm := batchRow()
	sm.Provider = p.ID()
	ready := batchPrepareCmd(context.Background(), 1, registry.NewWith(p), titler.Config{Provider: "agy"}, []model.Summary{sm}, nil)().(batchReadyMsg)
	if len(ready.items) != 1 || len(ready.frozen) != 0 {
		t.Fatalf("desktop source was not prepared for the configured agent: %+v", ready)
	}
}
