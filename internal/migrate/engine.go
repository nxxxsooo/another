package migrate

import (
	"context"
	"fmt"

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
	if existing, ok := FindDuplicate(e.Index, dst, conv); ok {
		if ens, ok := dst.(provider.ResumeEnsurer); ok && !opts.DryRun {
			if err := ens.EnsureResumable(conv, *existing); err != nil {
				return nil, fmt.Errorf("ensure resumable: %w", err)
			}
		}
		if e.Index != nil && !opts.DryRun {
			_ = e.Index.RecordMigration(dst.ID(), model.OriginDigest(conv), existing.SessionID, existing.StoragePath, conv.ID, conv.Provider)
		}
		return &Result{
			Source:        conv,
			Write:         existing,
			Resume:        dst.ResumeCommand(*existing),
			TargetName:    dst.DisplayName(),
			AlreadyExists: true,
		}, nil
	}
	write, err := dst.Write(ctx, conv, provider.WriteOpts{
		ProjectPath: opts.ProjectPath,
		DryRun:      opts.DryRun,
	})
	if err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	if !opts.DryRun && e.Index != nil {
		_ = e.Index.RecordMigration(dst.ID(), model.OriginDigest(conv), write.SessionID, write.StoragePath, conv.ID, conv.Provider)
		_, _ = index.UpdateIncremental(ctx, e.Registry, e.Index, dst.ID())
	}
	return &Result{
		Source:     conv,
		Write:      write,
		Resume:     dst.ResumeCommand(*write),
		TargetName: dst.DisplayName(),
	}, nil
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
