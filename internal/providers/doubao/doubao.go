// Package doubao reads Doubao Work conversations associated with a macOS
// desktop profile. The server owns conversations; .sessions identifies Work
// sessions that actually ran on this desktop, not a portable transcript store.
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
)

const ProviderID = "doubao"

type Provider struct {
	profile string
	chats   string
	base    string
	http    *http.Client
	auth    func(context.Context, string) (http.Header, error)
}

func New() *Provider {
	profile := config.EnvRootOrDefault("DOUBAO_PROFILE", filepath.Join(config.HomeDir(), "Library", "Application Support", "Doubao", "Profile 1"))
	return &Provider{
		profile: profile, chats: filepath.Join(config.HomeDir(), "Doubao", "chats"), base: apiBase, auth: desktopAuth,
		http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

func (p *Provider) ID() string                    { return ProviderID }
func (p *Provider) DisplayName() string           { return "Doubao Work" }
func (p *Provider) RequiresRemoteDiscovery() bool { return true }
func (p *Provider) sessionsRoot() string {
	return filepath.Join(p.profile, ".doubao", "agent_mode", "workspace", ".sessions")
}
func (p *Provider) Installed() bool {
	info, err := os.Stat(p.sessionsRoot())
	return err == nil && info.IsDir()
}
func (p *Provider) DefaultPaths() []provider.PathSpec {
	return []provider.PathSpec{{Label: "desktop profile", Path: p.profile, Env: "DOUBAO_PROFILE"}}
}
func (p *Provider) SupportsResume() bool                      { return false }
func (p *Provider) ResumeCommand(provider.WriteResult) string { return "" }
func (p *Provider) Write(context.Context, *model.Conversation, provider.WriteOpts) (*provider.WriteResult, error) {
	return nil, fmt.Errorf("doubao Work is a source-only adapter; native conversation import is not supported")
}

func (p *Provider) client(ctx context.Context) (*apiClient, error) {
	h, err := p.auth(ctx, p.profile)
	if err != nil {
		return nil, err
	}
	return &apiClient{http: p.http, base: p.base, headers: h}, nil
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

func (p *Provider) localIDs() (map[string]bool, error) {
	entries, err := os.ReadDir(p.sessionsRoot())
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() && validID(entry.Name()) {
			ids[entry.Name()] = true
		}
	}
	return ids, nil
}

func (p *Provider) nativeConversations(ctx context.Context, c *apiClient) ([]conversation, error) {
	ids, err := p.localIDs()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	rows, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	var out []conversation
	for _, row := range rows {
		if ids[row.ID] && row.Type == 3 && !row.Deleted {
			out = append(out, row)
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

func (p *Provider) summary(c conversation, m metadata, projectPath string) model.Summary {
	return model.Summary{
		ID: c.ID, Provider: ProviderID, ProjectPath: projectPath, Title: c.Name, Kind: model.SessionKindRoot,
		CreatedAt: seconds(m.Created), UpdatedAt: seconds(c.Updated),
		StoragePath: filepath.Join(p.sessionsRoot(), c.ID),
		// Native versions are microseconds; indexing them at nanosecond
		// precision catches message edits within the same update_time second.
		SourceMtime: int64(m.Version) * 1000,
	}
}

func (p *Provider) Discover(ctx context.Context, opts provider.DiscoverOpts) ([]model.Summary, error) {
	if opts.ProjectFilter != "" || !p.Installed() {
		return nil, nil
	}
	c, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := p.nativeConversations(ctx, c)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	meta, err := c.metadata(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Summary
	for _, row := range rows {
		m, found := meta[row.ID]
		if !found || m.Status == 0 {
			return nil, fmt.Errorf("doubao metadata is incomplete; keeping the existing index")
		}
		if m.Status != 1 { // Only the native active state, never retained archived/deleted directories.
			continue
		}
		projectPath, err := p.projectPath(row.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, p.summary(row, m, projectPath))
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

func (p *Provider) find(ctx context.Context, c *apiClient, ref provider.SessionRef) (conversation, error) {
	if !validID(ref.ID) {
		return conversation{}, provider.ErrNotFound
	}
	rows, err := p.nativeConversations(ctx, c)
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
	c, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	row, err := p.find(ctx, c, ref)
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
	projectPath, err := p.projectPath(row.ID)
	if err != nil {
		return nil, err
	}
	sm := p.summary(row, meta[row.ID], projectPath)
	return &model.Conversation{
		ID: sm.ID, Provider: ProviderID, ProjectPath: sm.ProjectPath, Title: sm.Title, StoragePath: sm.StoragePath,
		CreatedAt: sm.CreatedAt, UpdatedAt: sm.UpdatedAt, Messages: messages, MessageCount: len(messages),
	}, nil
}

func (p *Provider) RenameSession(ctx context.Context, ref provider.SessionRef, title string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("doubao title must not be empty")
	}
	c, err := p.client(ctx)
	if err != nil {
		return err
	}
	row, err := p.find(ctx, c, ref)
	if err != nil {
		return err
	}
	if !validID(row.Thread) {
		return fmt.Errorf("doubao returned an invalid thread identity")
	}
	var response struct {
		Code *int `json:"code"`
	}
	if err := c.post(ctx, "/samantha/thread/update", map[string]string{"thread_id": row.Thread, "name": title}, &response); err != nil {
		return err
	}
	if response.Code == nil {
		return fmt.Errorf("doubao rename response missing status")
	}
	if *response.Code != 0 {
		return fmt.Errorf("doubao rename rejected (code %d)", *response.Code)
	}
	updated, err := p.find(ctx, c, ref)
	if err != nil {
		return fmt.Errorf("%w: Doubao accepted rename but readback failed: %v", provider.ErrPartial, err)
	}
	if updated.Name != title {
		return fmt.Errorf("%w: Doubao accepted rename but the current title has not matched yet; refresh before retrying", provider.ErrPartial)
	}
	return nil
}
