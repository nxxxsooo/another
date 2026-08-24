package migrate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
)

type Options struct {
	FromProvider string
	ToProvider   string
	SessionID    string
	ProjectPath  string
	DryRun       bool
}

type Result struct {
	Source        *model.Conversation
	Write         *provider.WriteResult
	Resume        string
	TargetName    string
	AlreadyExists bool
	Warnings      []string
}

type Engine struct {
	Registry *registry.Registry
	Index    *index.Store
}

func (e *Engine) Run(ctx context.Context, opts Options) (*Result, error) {
	var sm *model.Summary
	var err error
	if opts.FromProvider != "" {
		sm, err = e.Index.Get(registry.NormalizeID(opts.FromProvider), opts.SessionID)
	} else {
		sm, err = e.Index.FindByID(opts.SessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve session: %w", err)
	}
	src, err := e.Registry.Get(sm.Provider)
	if err != nil {
		return nil, err
	}
	conv, err := src.Load(ctx, provider.SessionRef{
		ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
	})
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	dst, err := e.Registry.Get(registry.NormalizeID(opts.ToProvider))
	if err != nil {
		return nil, err
	}
	if !dst.Installed() {
		return nil, provider.ErrNotInstalled
	}
	return e.writeConversation(ctx, dst, conv, opts.ProjectPath, opts.DryRun)
}

// Import writes a portable conversation through the same deduplication,
// verification, cleanup, and bookkeeping path as a provider migration.
func (e *Engine) Import(ctx context.Context, conv *model.Conversation, opts Options) (*Result, error) {
	dst, err := e.Registry.Get(registry.NormalizeID(opts.ToProvider))
	if err != nil {
		return nil, err
	}
	if !dst.Installed() {
		return nil, provider.ErrNotInstalled
	}
	return e.writeConversation(ctx, dst, conv, opts.ProjectPath, opts.DryRun)
}

func (e *Engine) writeConversation(ctx context.Context, dst provider.Provider, conv *model.Conversation, project string, dryRun bool) (*Result, error) {
	var dedup DedupIndex
	if e.Index != nil {
		dedup = e.Index
	}
	existing, duplicate, err := FindDuplicateE(dedup, dst, conv)
	if err != nil {
		return nil, fmt.Errorf("check existing migration: %w", err)
	}
	if duplicate {
		var warnings []string
		if ens, ok := dst.(provider.ResumeEnsurer); ok && !dryRun {
			if err := ens.EnsureResumable(conv, *existing); err != nil {
				return nil, fmt.Errorf("ensure resumable: %w", err)
			}
		}
		if e.Index != nil && !dryRun {
			if err := e.Index.RecordMigrationSnapshot(dst.ID(), model.SnapshotDigest(conv), existing.SessionID, existing.StoragePath, conv.ID, conv.Provider, len(conv.Messages)); err != nil {
				warnings = append(warnings, "record migration: "+err.Error())
			}
		}
		return &Result{
			Source:        conv,
			Write:         existing,
			Resume:        dst.ResumeCommand(*existing),
			TargetName:    dst.DisplayName(),
			AlreadyExists: true,
			Warnings:      warnings,
		}, nil
	}
	write, err := dst.Write(ctx, conv, provider.WriteOpts{
		ProjectPath: project,
		DryRun:      dryRun,
	})
	if err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	if write == nil {
		return nil, fmt.Errorf("write: provider returned no result")
	}
	if !dryRun {
		if err := verifyWrite(ctx, dst, conv, *write); err != nil {
			cleanupErr := cleanupWrite(ctx, dst, *write)
			if cleanupErr != nil {
				return nil, errors.Join(err, fmt.Errorf("cleanup failed migration: %w", cleanupErr))
			}
			return nil, err
		}
	}
	var warnings []string
	if !dryRun && e.Index != nil {
		if err := e.Index.RecordMigrationSnapshot(dst.ID(), model.SnapshotDigest(conv), write.SessionID, write.StoragePath, conv.ID, conv.Provider, len(conv.Messages)); err != nil {
			warnings = append(warnings, "record migration: "+err.Error())
		}
		if _, err := index.UpdateIncremental(ctx, e.Registry, e.Index, dst.ID()); err != nil {
			warnings = append(warnings, "update index: "+err.Error())
		}
	}
	return &Result{
		Source:     conv,
		Write:      write,
		Resume:     dst.ResumeCommand(*write),
		TargetName: dst.DisplayName(),
		Warnings:   warnings,
	}, nil
}

func verifyWrite(ctx context.Context, dst provider.Provider, source *model.Conversation, write provider.WriteResult) error {
	loaded, err := dst.Load(ctx, provider.SessionRef{
		ID: write.SessionID, Provider: dst.ID(), StoragePath: write.StoragePath,
		ProjectPath: write.ProjectPath,
	})
	if err != nil {
		return fmt.Errorf("verify migrated session: %w", err)
	}
	// A zero source timestamp is deliberately unspecified. Providers may need to
	// synthesize one for native schemas, so mask only those corresponding values.
	loadedForDigest := *loaded
	loadedForDigest.Messages = append([]model.Message(nil), loaded.Messages...)
	var sourceConversationIndex int
	for i := range loadedForDigest.Messages {
		if loadedForDigest.Messages[i].Role != model.RoleUser && loadedForDigest.Messages[i].Role != model.RoleAssistant {
			continue
		}
		for sourceConversationIndex < len(source.Messages) && source.Messages[sourceConversationIndex].Role != model.RoleUser && source.Messages[sourceConversationIndex].Role != model.RoleAssistant {
			sourceConversationIndex++
		}
		if sourceConversationIndex < len(source.Messages) && source.Messages[sourceConversationIndex].Timestamp.IsZero() {
			loadedForDigest.Messages[i].Timestamp = time.Time{}
		}
		sourceConversationIndex++
	}
	want, got := model.ContentDigest(source), model.ContentDigest(&loadedForDigest)
	if want != got {
		return fmt.Errorf("verify migrated session: content digest mismatch (want %s, got %s)", want, got)
	}
	wantMeta := model.NewMigrationMeta(source)
	if loaded.Migration == nil || loaded.Migration.OriginID != wantMeta.OriginID ||
		loaded.Migration.OriginSource != wantMeta.OriginSource ||
		loaded.Migration.OriginDigest != wantMeta.OriginDigest {
		return fmt.Errorf("verify migrated session: migration marker mismatch")
	}
	return nil
}

func cleanupWrite(ctx context.Context, dst provider.Provider, write provider.WriteResult) error {
	cleaner, ok := dst.(provider.WriteCleaner)
	if !ok {
		return fmt.Errorf("%s does not support exact artifact cleanup; target may remain at %s", dst.ID(), write.StoragePath)
	}
	return cleaner.CleanupWrite(ctx, write)
}

func ResolveSession(ctx context.Context, reg *registry.Registry, idx *index.Store, id, from string) (*model.Summary, provider.Provider, error) {
	var sm *model.Summary
	var err error
	if from != "" {
		sm, err = idx.Get(registry.NormalizeID(from), id)
	} else {
		sm, err = idx.FindByID(id)
	}
	if err != nil {
		return nil, nil, err
	}
	p, err := reg.Get(sm.Provider)
	if err != nil {
		return nil, nil, err
	}
	return sm, p, nil
}
