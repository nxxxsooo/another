package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
	"github.com/CyrusSE/agenthop/internal/util"
)

type Options struct {
	FromProvider string
	ToProvider   string
	SessionID    string
	ProjectPath  string
	DryRun       bool
	ContextMode  ContextMode
}

type Result struct {
	Source            *model.Conversation
	Write             *provider.WriteResult
	Resume            string
	TargetName        string
	AlreadyExists     bool
	Warnings          []string
	ContextMode       ContextMode
	SourceCount       int
	CleanedCount      int
	ProjectedCount    int
	ExceedsSafeLimits bool
}

type ContextMode string

const (
	ContextAuto   ContextMode = "auto"
	ContextFull   ContextMode = "full"
	ContextRecent ContextMode = "recent"
)

func ParseContextMode(value string) (ContextMode, error) {
	mode := ContextMode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		mode = ContextAuto
	}
	switch mode {
	case ContextAuto, ContextFull, ContextRecent:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid context mode %q (use auto, full, or recent)", value)
	}
}

type Engine struct {
	Registry *registry.Registry
	Index    *index.Store
}

func (e *Engine) Run(ctx context.Context, opts Options) (*Result, error) {
	mode, err := ParseContextMode(string(opts.ContextMode))
	if err != nil {
		return nil, err
	}
	var sm *model.Summary

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
	return e.writeConversationMode(ctx, dst, conv, opts.ProjectPath, opts.DryRun, mode)
}

// Import writes a portable conversation through the same deduplication,
// verification, cleanup, and bookkeeping path as a provider migration.
func (e *Engine) Import(ctx context.Context, conv *model.Conversation, opts Options) (*Result, error) {
	mode, err := ParseContextMode(string(opts.ContextMode))
	if err != nil {
		return nil, err
	}
	dst, err := e.Registry.Get(registry.NormalizeID(opts.ToProvider))
	if err != nil {
		return nil, err
	}
	if !dst.Installed() {
		return nil, provider.ErrNotInstalled
	}
	return e.writeConversationMode(ctx, dst, conv, opts.ProjectPath, opts.DryRun, mode)
}

func (e *Engine) writeConversation(ctx context.Context, dst provider.Provider, conv *model.Conversation, project string, dryRun bool) (*Result, error) {
	return e.writeConversationMode(ctx, dst, conv, project, dryRun, ContextAuto)
}

func (e *Engine) writeConversationMode(ctx context.Context, dst provider.Provider, conv *model.Conversation, project string, dryRun bool, mode ContextMode) (*Result, error) {
	writeConv, projection := projectConversation(conv, mode)
	meta := model.NewMigrationMeta(conv)
	meta.ContextMode = string(mode)
	meta.OriginDigest = model.MigrationContextDigest(conv, string(mode))
	writeConv.WriteMigration = &meta
	dedupSource := cloneConversation(conv)
	dedupSource.WriteMigration = &meta
	var dedup DedupIndex
	if e.Index != nil {
		dedup = e.Index
	}
	existing, duplicate, err := FindDuplicateE(dedup, dst, dedupSource)
	if err != nil {
		return nil, fmt.Errorf("check existing migration: %w", err)
	}
	if duplicate {
		var warnings []string
		if ens, ok := dst.(provider.ResumeEnsurer); ok && !dryRun {
			if err := ens.EnsureResumable(writeConv, *existing); err != nil {
				return nil, fmt.Errorf("ensure resumable: %w", err)
			}
		}
		if e.Index != nil && !dryRun {
			if err := e.Index.RecordMigrationSnapshot(dst.ID(), meta.OriginDigest, existing.SessionID, existing.StoragePath, conv.ID, conv.Provider, len(conv.Messages)); err != nil {
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
			ContextMode:   mode, SourceCount: projection.SourceCount, CleanedCount: projection.CleanedCount,
			ProjectedCount: projection.ProjectedCount, ExceedsSafeLimits: projection.ExceedsSafeLimits,
		}, nil
	}
	write, err := dst.Write(ctx, writeConv, provider.WriteOpts{
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
		if err := verifyWrite(ctx, dst, writeConv, *write); err != nil {
			cleanupErr := cleanupWrite(ctx, dst, *write)
			if cleanupErr != nil {
				return nil, errors.Join(err, fmt.Errorf("cleanup failed migration: %w", cleanupErr))
			}
			return nil, err
		}
	}
	var warnings []string
	if projection.Warning != "" {
		warnings = append(warnings, projection.Warning)
	}
	if !dryRun && e.Index != nil {
		if err := e.Index.RecordMigrationSnapshot(dst.ID(), meta.OriginDigest, write.SessionID, write.StoragePath, conv.ID, conv.Provider, len(conv.Messages)); err != nil {
			warnings = append(warnings, "record migration: "+err.Error())
		}
		if _, err := index.UpdateIncremental(ctx, e.Registry, e.Index, dst.ID()); err != nil {
			warnings = append(warnings, "update index: "+err.Error())
		}
	}
	return &Result{
		Source:      conv,
		Write:       write,
		Resume:      dst.ResumeCommand(*write),
		TargetName:  dst.DisplayName(),
		Warnings:    warnings,
		ContextMode: mode, SourceCount: projection.SourceCount, CleanedCount: projection.CleanedCount,
		ProjectedCount: projection.ProjectedCount, ExceedsSafeLimits: projection.ExceedsSafeLimits,
	}, nil
}

const (
	resumeMessageLimit = 48
	resumeRuneLimit    = 64000
	resumeMessageRunes = 16000
)

func cloneConversation(source *model.Conversation) *model.Conversation {
	cloned := *source
	cloned.Messages = append([]model.Message(nil), source.Messages...)
	return &cloned
}

type ProjectionInfo struct {
	SourceCount       int
	CleanedCount      int
	ProjectedCount    int
	ExceedsSafeLimits bool
	Warning           string
}

// projectConversation removes provider control noise, then selects the full
// or bounded recent conversation according to mode.
func projectConversation(source *model.Conversation, mode ContextMode) (*model.Conversation, ProjectionInfo) {
	cleaned := cleanConversation(source)
	info := ProjectionInfo{SourceCount: len(source.Messages), CleanedCount: len(cleaned.Messages)}
	info.ExceedsSafeLimits = !withinResumeLimits(cleaned.Messages)
	if mode == ContextFull || (mode == ContextAuto && !info.ExceedsSafeLimits) {
		info.ProjectedCount = len(cleaned.Messages)
		if mode == ContextFull && info.ExceedsSafeLimits {
			info.Warning = fmt.Sprintf("full context has %d messages and exceeds safe resume limits; %s may compact or reject it", len(cleaned.Messages), source.Provider)
		}
		return cleaned, info
	}
	projected, changed := recentConversation(source, cleaned)
	info.ProjectedCount = len(projected.Messages)
	if changed {
		info.Warning = fmt.Sprintf("resume context reduced from %d source messages (%d cleaned) to %d; retrieve older details with agenthop search/show/export using %s session %s", info.SourceCount, info.CleanedCount, info.ProjectedCount, source.Provider, source.ID)
	}
	return projected, info
}

func cleanConversation(source *model.Conversation) *model.Conversation {
	cleaned := cloneConversation(source)
	cleaned.Messages = make([]model.Message, 0, len(source.Messages))
	for _, message := range source.Messages {
		if message.Role != model.RoleUser && message.Role != model.RoleAssistant {
			continue
		}
		text := message.PlainText()
		if message.Role == model.RoleAssistant && strings.TrimSpace(text) == "[REDACTED]" {
			continue
		}
		if message.Role == model.RoleUser {
			normalized, ok := util.NormalizeUserText(text)
			if !ok {
				continue
			}
			text = normalized
		}
		if text == "" {
			continue
		}
		message.Content, message.Blocks = text, nil
		cleaned.Messages = append(cleaned.Messages, message)
	}
	cleaned.MessageCount = len(cleaned.Messages)
	return cleaned
}

func withinResumeLimits(messages []model.Message) bool {
	if len(messages) > resumeMessageLimit {
		return false
	}
	total := 0
	for _, message := range messages {
		size := utf8.RuneCountInString(message.Content)
		if size > resumeMessageRunes {
			return false
		}
		total += size
	}
	return total <= resumeRuneLimit
}

func recentConversation(source, cleaned *model.Conversation) (*model.Conversation, bool) {
	projected := cloneConversation(cleaned)
	candidates := append([]model.Message(nil), cleaned.Messages...)
	changed := false
	for i := range candidates {
		if utf8.RuneCountInString(candidates[i].Content) <= resumeMessageRunes {
			continue
		}
		runes := []rune(candidates[i].Content)
		half := resumeMessageRunes / 2
		candidates[i].Content = string(runes[:half]) + "\n\n[... message shortened by Agenthop for resumable context ...]\n\n" + string(runes[len(runes)-half:])
		changed = true
	}

	start, runes := len(candidates), 0
	for start > 0 && len(candidates)-start < resumeMessageLimit {
		size := utf8.RuneCountInString(candidates[start-1].Content)
		if runes > 0 && runes+size > resumeRuneLimit {
			break
		}
		start--
		runes += size
	}
	if start > 0 {
		changed = true
	}
	selected := append([]model.Message(nil), candidates[start:]...)
	firstUser := -1
	for i := range selected {
		if selected[i].Role == model.RoleUser {
			firstUser = i
			break
		}
	}
	if firstUser > 0 {
		selected = selected[firstUser:]
		changed = true
	} else if firstUser < 0 {
		for i := start - 1; i >= 0; i-- {
			if candidates[i].Role == model.RoleUser {
				selected = append([]model.Message{candidates[i]}, selected...)
				changed = true
				break
			}
		}
	}
	if !changed {
		projected.Messages = selected
		projected.MessageCount = len(selected)
		return projected, false
	}
	handoff := model.Message{
		Role:      model.RoleUser,
		Content:   fmt.Sprintf("[Agenthop migration handoff]\nThis is a bounded recent-context view of %s session %s (%q). Older history and provider control records remain in the source session. Continue from the recent turns below; do not restart the task.", source.Provider, source.ID, source.Title),
		Timestamp: source.CreatedAt,
	}
	projected.Messages = append([]model.Message{handoff}, selected...)
	projected.MessageCount = len(projected.Messages)
	return projected, true
}

// resumableConversation preserves the pre-context-mode helper for focused
// tests and internal callers; it is equivalent to recent mode.
func resumableConversation(source *model.Conversation) (*model.Conversation, string) {
	projected, info := projectConversation(source, ContextRecent)
	return projected, info.Warning
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
