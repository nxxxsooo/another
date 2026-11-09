// Package doubao reads account-scoped native Doubao CLI and desktop Work
// conversations. The server owns transcripts; local session artifacts only
// establish scope and are never rewritten by this adapter.
package doubao

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
)

const ProviderID = "doubao"

type Provider struct {
	profile, profileRoot, chats, base string
	http                              *http.Client
	auth                              func(context.Context, string) (http.Header, error)
	cliInstalled                      func() bool
	listCLI                           func(context.Context) (map[string]string, error)
}

func New() *Provider {
	return &Provider{
		// An explicit account profile must never silently fall back, even when
		// it points at an unavailable or temporary directory.
		profile:     config.ExpandPath(strings.TrimSpace(os.Getenv("DOUBAO_PROFILE"))),
		profileRoot: filepath.Join(config.HomeDir(), "Library", "Application Support", "Doubao"),
		chats:       filepath.Join(config.HomeDir(), "Doubao", "chats"), base: apiBase, auth: desktopAuth,
		cliInstalled: cliAvailable, listCLI: cliList,
		http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (p *Provider) ID() string                    { return ProviderID }
func (p *Provider) DisplayName() string           { return "Doubao" }
func (p *Provider) RequiresRemoteDiscovery() bool { return true }
func sessionsRoot(profile string) string {
	return filepath.Join(profile, ".doubao", "agent_mode", "workspace", ".sessions")
}

// Installed is deliberately local-only and does not open Keychain or connect
// to either CLI/cloud service. Detection does not imply a usable login.
func (p *Provider) Installed() bool {
	if p.cliInstalled() {
		return true
	}
	if p.profile != "" {
		return hasCookies(p.profile)
	}
	entries, _ := os.ReadDir(p.profileRoot)
	for _, e := range entries {
		if e.IsDir() && (e.Name() == "Default" || strings.HasPrefix(e.Name(), "Profile ")) && hasCookies(filepath.Join(p.profileRoot, e.Name())) {
			return true
		}
	}
	return false
}

func (p *Provider) DefaultPaths() []provider.PathSpec {
	path := p.profile
	if path == "" {
		path = p.profileRoot
	}
	return []provider.PathSpec{{Label: "desktop profile (cloud authentication)", Path: path, Env: "DOUBAO_PROFILE"}}
}
func (p *Provider) SupportsResume() bool { return p.cliInstalled() }
func (p *Provider) ResumeCommand(r provider.WriteResult) string {
	if !p.SupportsResume() || !validID(r.SessionID) {
		return ""
	}
	cmd := "doubao --resume " + util.QuoteArg(r.SessionID)
	if r.ProjectPath != "" {
		cmd += " -C " + util.QuoteArg(r.ProjectPath)
	}
	return cmd
}
func (p *Provider) Write(context.Context, *model.Conversation, provider.WriteOpts) (*provider.WriteResult, error) {
	return nil, fmt.Errorf("doubao is a source-only adapter; native conversation import is not supported")
}

// CloseIdleConnections keeps the long-lived event listener from showing an idle
// authenticated socket between turns. Each completed turn is an isolated fetch.
func (p *Provider) CloseIdleConnections() { p.http.CloseIdleConnections() }

func (p *Provider) client(ctx context.Context) (*apiClient, string, error) {
	profile, err := selectProfile(p.profileRoot, p.profile)
	if err != nil {
		return nil, "", err
	}
	h, err := p.auth(ctx, profile)
	if err != nil {
		return nil, "", err
	}
	return &apiClient{http: p.http, base: p.base, headers: h}, profile, nil
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 32 {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func localIDs(profile string) (map[string]bool, error) {
	entries, err := os.ReadDir(sessionsRoot(profile))
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() && validID(e.Name()) {
			ids[e.Name()] = true
		}
	}
	return ids, nil
}

// Local session directories can be stale or absent; they are not an active
// conversation catalog. Intersect exact CLI identities with the selected desktop
// account before loading or renaming; never match titles or import all cloud chats.
func (p *Provider) nativeConversations(ctx context.Context, c *apiClient, profile string) ([]conversation, error) {
	ids, err := localIDs(profile)
	if err != nil {
		return nil, err
	}
	desktopIDs := make(map[string]bool, len(ids))
	for id := range ids {
		desktopIDs[id] = true
	}
	cliIDs := map[string]string{}
	if p.cliInstalled() {
		cliIDs, err = p.listCLI(ctx)
		if err != nil {
			return nil, err
		}
		for id := range cliIDs {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	var out []conversation
	matched := map[string]bool{}
	for _, row := range rows {
		// CLI --list also includes non-Work entries (for example type-1
		// enterprise bots). They prove account overlap but are not indexed.
		matched[row.ID] = true
		if ids[row.ID] && row.Type == 3 && !row.Deleted {
			row.DesktopLocal = desktopIDs[row.ID]
			out = append(out, row)
		}
	}
	for id := range cliIDs {
		if !matched[id] {
			return nil, fmt.Errorf("doubao CLI sessions are unavailable in the selected desktop account; sign both clients into the same account or set DOUBAO_PROFILE")
		}
	}
	return out, nil
}

func seconds(n integer) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(n), 0).UTC()
}
func (p *Provider) summary(row conversation, m metadata, profile string) model.Summary {
	storage := "doubao://conversation/" + row.ID
	if row.DesktopLocal {
		storage = filepath.Join(sessionsRoot(profile), row.ID)
	}
	return model.Summary{ID: row.ID, Provider: ProviderID, Title: row.Name, Kind: model.SessionKindRoot,
		CreatedAt: seconds(m.Created), UpdatedAt: seconds(row.Updated),
		StoragePath: storage, SourceMtime: int64(m.Version) * 1000}
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.Installed() {
		return nil, nil
	}
	c, profile, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := p.nativeConversations(ctx, c, profile)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	meta, err := c.metadata(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Summary
	for _, row := range rows {
		m, ok := meta[row.ID]
		if !ok || m.Status == 0 {
			return nil, fmt.Errorf("doubao metadata is incomplete; keeping the existing index")
		}
		if m.Status != 1 {
			continue
		}
		sm := p.summary(row, m, profile)
		if row.DesktopLocal {
			sm.ProjectPath, err = p.projectPath(profile, row.ID)
			if err != nil {
				return nil, err
			}
		}
		if opts.ProjectFilter != "" && filepath.Clean(opts.ProjectFilter) != filepath.Clean(sm.ProjectPath) {
			continue
		}
		out = append(out, sm)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

func (p *Provider) find(ctx context.Context, c *apiClient, profile string, ref provider.SessionRef) (conversation, error) {
	if !validID(ref.ID) {
		return conversation{}, provider.ErrNotFound
	}
	rows, err := p.nativeConversations(ctx, c, profile)
	if err != nil {
		return conversation{}, err
	}
	for _, row := range rows {
		if row.ID == ref.ID {
			return row, nil
		}
	}
	return conversation{}, provider.ErrNotFound
}

func (p *Provider) NativeTitle(ctx context.Context, ref provider.SessionRef) (string, error) {
	c, profile, err := p.client(ctx)
	if err != nil {
		return "", err
	}
	row, err := p.find(ctx, c, profile, ref)
	return row.Name, err
}
func (p *Provider) Load(ctx context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	return p.load(ctx, ref, 0)
}
func (p *Provider) LoadPreview(ctx context.Context, ref provider.SessionRef, limit int) (*model.Conversation, error) {
	if limit <= 0 {
		limit = 20
	}
	return p.load(ctx, ref, limit)
}
func (p *Provider) load(ctx context.Context, ref provider.SessionRef, limit int) (*model.Conversation, error) {
	c, profile, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	row, err := p.find(ctx, c, profile, ref)
	if err != nil {
		return nil, err
	}
	meta, err := c.metadata(ctx)
	if err != nil {
		return nil, err
	}
	if meta[row.ID].Status != 1 {
		return nil, provider.ErrNotFound
	}
	messages, err := c.messages(ctx, row.ID, limit)
	if err != nil {
		return nil, err
	}
	sm := p.summary(row, meta[row.ID], profile)
	if row.DesktopLocal {
		sm.ProjectPath, err = p.projectPath(profile, row.ID)
		if err != nil {
			return nil, err
		}
	}
	return &model.Conversation{ID: sm.ID, Provider: ProviderID, ProjectPath: sm.ProjectPath, Title: sm.Title, StoragePath: sm.StoragePath,
		CreatedAt: sm.CreatedAt, UpdatedAt: sm.UpdatedAt, Messages: messages, MessageCount: len(messages)}, nil
}

func (p *Provider) RenameSession(ctx context.Context, ref provider.SessionRef, title string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("doubao title must not be empty")
	}
	c, profile, err := p.client(ctx)
	if err != nil {
		return err
	}
	row, err := p.find(ctx, c, profile, ref)
	if err != nil {
		return err
	}
	if !validID(row.Thread) {
		return fmt.Errorf("doubao returned an invalid thread identity")
	}
	var response struct {
		Code *int `json:"code"`
	}
	if err := c.post(ctx, "/samantha/thread/update", map[string]string{"thread_id": row.Thread, "conversation_id": row.ID, "name": title}, &response); err != nil {
		return err
	}
	if response.Code == nil {
		return fmt.Errorf("doubao rename response missing status")
	}
	if *response.Code != 0 {
		return fmt.Errorf("doubao rename rejected (code %d)", *response.Code)
	}
	updated, err := p.find(ctx, c, profile, ref)
	if err != nil {
		return fmt.Errorf("%w: Doubao accepted rename but readback failed: %v", provider.ErrPartial, err)
	}
	if updated.Name != title {
		return fmt.Errorf("%w: Doubao accepted rename but the current title has not matched yet; refresh before retrying", provider.ErrPartial)
	}
	return nil
}
