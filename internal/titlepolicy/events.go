package titlepolicy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titlemark"
	"github.com/nxxxsooo/another/internal/titler"
)

// Validate checks local prerequisites without scanning conversations or invoking
// a model. The caller checks its event source separately before installation.
func Validate(settings config.Settings, reg *registry.Registry) error {
	if settings.TitleModel == nil || settings.TitleModel.Provider == "" {
		return fmt.Errorf("no title agent configured: run another setup")
	}
	if !titler.Supports(settings.TitleModel.Provider) || !titler.Available(settings.TitleModel.Provider) {
		return fmt.Errorf("configured title agent %s is unavailable", settings.TitleModel.Provider)
	}
	p, err := reg.Get("doubao")
	if err != nil {
		return err
	}
	if _, ok := p.(provider.SessionRenamer); !ok {
		return fmt.Errorf("doubao native rename unavailable")
	}
	if _, ok := p.(provider.NativeTitler); !ok {
		return fmt.Errorf("doubao native title readback unavailable")
	}
	if !p.Installed() {
		return fmt.Errorf("doubao authenticated profile unavailable")
	}
	return nil
}

// OnEvent loads only the conversation named by a completed-turn event. It does
// not consult or refresh the index or schedule retries. The provider still
// verifies account membership through its native catalog before loading the ID.
func OnEvent(ctx context.Context, reg *registry.Registry, conversationID string) error {
	return onEvent(ctx, reg, conversationID, nil)
}

func onEvent(ctx context.Context, reg *registry.Registry, id string, suggest func(context.Context, titler.Config, titler.Request) (string, error)) (resultErr error) {
	settings, err := config.LoadSettings()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !settings.TitlePolicy.DoubaoEvents {
		return nil
	}
	enabled := false
	for _, pid := range settings.EnabledProviders {
		if pid == "doubao" {
			enabled = true
		}
	}
	if !enabled {
		return nil
	}
	status := Status{At: time.Now().UTC()}
	if previous, err := LoadStatus(); err == nil {
		status.LastRenamedAt = previous.LastRenamedAt
		status.LastRenamedTitle = previous.LastRenamedTitle
	}
	defer func() {
		if resultErr != nil {
			status.Failed = 1
			status.Error = "event title naming failed; check the configured title agent and Doubao authentication"
			if errors.Is(resultErr, context.Canceled) {
				status.Error = "event title naming cancelled"
			}
		}
		if err := saveStatus(status); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("save event title status: %w", err))
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("event has no conversation id")
	}
	if settings.TitlePolicy.DoubaoEnabledAt <= 0 {
		return fmt.Errorf("doubao event policy has no consent cutoff; enable it again")
	}
	if settings.TitleModel == nil || settings.TitleModel.Provider == "" {
		return fmt.Errorf("no title agent configured: run another setup")
	}
	p, err := reg.Get("doubao")
	if err != nil {
		return err
	}
	if closer, ok := p.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	renamer, ok := p.(provider.SessionRenamer)
	if !ok {
		return fmt.Errorf("doubao native rename unavailable")
	}
	if _, ok := p.(provider.NativeTitler); !ok {
		return fmt.Errorf("doubao native title readback unavailable")
	}
	db, err := sql.Open("sqlite", filepath.Join(config.ConfigDir(), "title-policy-lock.db")+"?_pragma=busy_timeout(1000)&_txlock=immediate")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("doubao title policy busy: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ref := provider.SessionRef{ID: id, Provider: "doubao"}
	conv, err := p.Load(ctx, ref)
	if err != nil {
		return err
	}
	if conv == nil {
		return fmt.Errorf("event conversation unavailable")
	}
	status.Checked = 1
	hasUser := false
	latest := model.Role("")
	for _, msg := range conv.Messages {
		if strings.TrimSpace(msg.PlainText()) == "" {
			continue
		}
		if msg.Role == model.RoleUser {
			hasUser = true
		}
		if msg.Role == model.RoleUser || msg.Role == model.RoleAssistant {
			latest = msg.Role
		}
	}
	if !hasUser || latest != model.RoleAssistant {
		return nil
	}
	marks := titlemark.Open()
	if marks.Load("doubao", id) == nil && (conv.CreatedAt.Unix() <= 0 || conv.CreatedAt.Unix() < settings.TitlePolicy.DoubaoEnabledAt) {
		return nil
	}
	sm := model.Summary{ID: id, Provider: "doubao", Title: conv.Title, ProjectPath: conv.ProjectPath, StoragePath: conv.StoragePath, CreatedAt: conv.CreatedAt, UpdatedAt: conv.UpdatedAt}
	ref.StoragePath, ref.ProjectPath = sm.StoragePath, sm.ProjectPath
	if dynamic, ok := p.(provider.SessionCapabilityProvider); ok && !dynamic.Capabilities(ref).Rename {
		return fmt.Errorf("event session does not support native rename")
	}
	cfg := titler.Config{Provider: settings.TitleModel.Provider, Model: settings.TitleModel.Model, Language: titler.NormalizeLanguage(titler.Language(settings.TitleModel.Language))}
	return Rename(ctx, p, renamer, sm, Options{Config: cfg, Marks: marks, ReplaceNative: true, Suggest: suggest,
		Apply: func(ctx context.Context, r provider.SessionRenamer, sm model.Summary, title string) error {
			err := r.RenameSession(ctx, provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}, title)
			if err == nil {
				status.Renamed++
				status.LastRenamedAt = status.At
				status.LastRenamedTitle = title
			}
			return err
		},
	})
}
