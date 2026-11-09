// Package titlepolicy shares stale-aware native title generation across hooks and refreshes.
package titlepolicy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/titlemark"
	"github.com/nxxxsooo/another/internal/titler"
)

type Options struct {
	Config                titler.Config
	Marks                 titlemark.Store
	ReplaceNative, DryRun bool
	Suggest               func(context.Context, titler.Config, titler.Request) (string, error)
	Apply                 func(context.Context, provider.SessionRenamer, model.Summary, string) error
	Log                   func(string, ...any)
}

// Rename uses the same policy for hooks and refreshes. Apply is called only after
// a second native read confirms that a model call did not race a manual edit.
func Rename(ctx context.Context, p provider.Provider, renamer provider.SessionRenamer, sm model.Summary, o Options) error {
	ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
	if err := ctx.Err(); err != nil {
		return err
	}
	conv, err := p.Load(ctx, ref)
	if err != nil {
		return err
	}
	readTitle := func() (string, error) {
		if native, ok := p.(provider.NativeTitler); ok {
			return native.NativeTitle(ctx, ref)
		}
		live, err := p.Load(ctx, ref)
		if err != nil {
			return "", err
		}
		if live == nil {
			return "", fmt.Errorf("missing live session")
		}
		return live.Title, nil
	}
	current := strings.TrimSpace(sm.Title)
	if _, ok := p.(provider.NativeTitler); ok {
		current, err = readTitle()
		if err != nil {
			return err
		}
	}
	var msgs []model.Message
	activity := sm.UpdatedAt
	if conv != nil {
		msgs = conv.Messages
		if conv.UpdatedAt.After(activity) {
			activity = conv.UpdatedAt
		}
	}
	if activity.Unix() <= 0 {
		activity = sm.CreatedAt
	}
	if activity.Unix() <= 0 {
		return fmt.Errorf("session has no authoritative title date")
	}
	turns := titler.UserTurns(msgs)
	log := o.Log
	if log == nil {
		log = func(string, ...any) {}
	}
	remember := func(title string) error {
		if o.DryRun {
			return nil
		}
		return o.Marks.Save(sm.Provider, sm.ID, titler.Mark{Title: title, UserTurns: turns, At: time.Now()})
	}
	decision := titler.Decide(titler.Freshness{Title: current, Mark: o.Marks.Load(sm.Provider, sm.ID), UserTurns: turns, Activity: activity, Language: o.Config.Language, ReplaceNative: o.ReplaceNative})
	switch decision.Plan {
	case titler.PlanSkip:
		log("Unchanged: %s keeps %q (%s)\n", sm.ID, current, decision.Reason)
		return nil
	case titler.PlanAdopt:
		log("Unchanged: %s keeps %q (%s)\n", sm.ID, current, decision.Reason)
		return remember(current)
	}
	refreshing := decision.Plan == titler.PlanRefresh
	suggest := o.Suggest
	if suggest == nil {
		suggest = titler.SuggestRetrying
	}
	suggestion, err := suggest(ctx, o.Config, titler.Request{Title: current, ProjectPath: sm.ProjectPath, CreatedAt: sm.CreatedAt, Date: activity, Messages: msgs, Refresh: refreshing, RequireTitle: p.ID() == "doubao" && !refreshing})
	if err != nil {
		return err
	}
	title := titler.Redate(suggestion, activity)
	switch {
	case title == "" && !refreshing:
		return fmt.Errorf("%s returned no title matching the policy", o.Config.Provider)
	case refreshing && (title == "" || titler.Topic(title) == titler.Topic(current)):
		title = titler.Redate(current, activity)
	}
	if o.DryRun {
		log("Dry run OK: would rename %s to %q (%s)\n", sm.ID, title, decision.Reason)
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	live, err := readTitle()
	if err != nil {
		return err
	}
	if live != current {
		log("Unchanged: %s was renamed while generating\n", sm.ID)
		return nil
	}
	if title != current {
		sm.Title = current
		if err := o.Apply(ctx, renamer, sm, title); err != nil {
			return err
		}
	} else {
		log("Unchanged: %s still fits (%s)\n", sm.ID, title, decision.Reason)
	}
	return remember(title)
}
