package titlepolicy

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titlemark"
	"github.com/nxxxsooo/another/internal/titler"
)

type eventProvider struct {
	provider.Provider
	conv                 model.Conversation
	loads, writes, scans int
	fail                 bool
}

func (p *eventProvider) ID() string { return "doubao" }
func (p *eventProvider) Load(_ context.Context, ref provider.SessionRef) (*model.Conversation, error) {
	p.loads++
	if ref.ID != p.conv.ID {
		return nil, errors.New("wrong target")
	}
	copy := p.conv
	return &copy, nil
}
func (p *eventProvider) NativeTitle(context.Context, provider.SessionRef) (string, error) {
	return p.conv.Title, nil
}
func (p *eventProvider) Discover(context.Context, provider.DiscoverOpts) ([]model.Summary, error) {
	p.scans++
	return nil, errors.New("event must not scan")
}
func (p *eventProvider) RenameSession(_ context.Context, _ provider.SessionRef, title string) error {
	p.writes++
	if p.fail {
		return errors.New("SECRET write failure")
	}
	p.conv.Title = title
	return nil
}

func eventFixture(t *testing.T) (*eventProvider, *registry.Registry, config.Settings) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	at := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	p := &eventProvider{conv: model.Conversation{ID: "target", Title: "test", ProjectPath: t.TempDir(), CreatedAt: at, UpdatedAt: at, Messages: []model.Message{{Role: model.RoleUser, Content: "test"}, {Role: model.RoleAssistant, Content: "connected"}}}}
	s := config.Settings{EnabledProviders: []string{"doubao"}, TitleModel: &config.TitleModel{Provider: "codex", Language: "en"}, TitlePolicy: config.TitlePolicy{DoubaoEvents: true, DoubaoEnabledAt: at.Unix() - 1}}
	return p, registry.NewWith(p), s
}
func saveEventSettings(t *testing.T, s config.Settings) {
	t.Helper()
	if err := config.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
}
func TestEventTargetOnlyNamesOnceAndPreservesManualChanges(t *testing.T) {
	p, reg, s := eventFixture(t)
	saveEventSettings(t, s)
	calls := 0
	suggest := func(_ context.Context, _ titler.Config, r titler.Request) (string, error) {
		calls++
		if !r.RequireTitle || r.Refresh || titler.MMDD(r.Date) != "1109" {
			t.Fatalf("request=%+v", r)
		}
		return "1109｜Explore｜Conversation Test", nil
	}
	for range 2 {
		if err := onEvent(context.Background(), reg, "target", suggest); err != nil {
			t.Fatal(err)
		}
	}
	p.conv.Title = "manual"
	p.conv.UpdatedAt = p.conv.UpdatedAt.Add(48 * time.Hour)
	if err := onEvent(context.Background(), reg, "target", suggest); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || p.writes != 1 || p.scans != 0 {
		t.Fatalf("calls=%d writes=%d scans=%d", calls, p.writes, p.scans)
	}
	status, err := LoadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Renamed != 0 || status.LastRenamedAt.IsZero() || status.LastRenamedTitle != "1109｜Explore｜Conversation Test" {
		t.Fatalf("later no-op event hid the rename: %+v", status)
	}
}
func TestEventEligibility(t *testing.T) {
	for _, which := range []string{"disabled", "provider disabled", "old", "old conforming", "no user", "unfinished"} {
		t.Run(which, func(t *testing.T) {
			p, reg, s := eventFixture(t)
			switch which {
			case "disabled":
				s.TitlePolicy.DoubaoEvents = false
			case "provider disabled":
				s.EnabledProviders = nil
			case "old":
				s.TitlePolicy.DoubaoEnabledAt = p.conv.CreatedAt.Unix() + 1
			case "old conforming":
				s.TitlePolicy.DoubaoEnabledAt = p.conv.CreatedAt.Unix() + 1
				p.conv.Title = "1109｜Explore｜Manual Title"
			case "no user":
				p.conv.Messages = p.conv.Messages[1:]
			case "unfinished":
				p.conv.Messages = p.conv.Messages[:1]
			}
			saveEventSettings(t, s)
			if err := onEvent(context.Background(), reg, "target", func(context.Context, titler.Config, titler.Request) (string, error) {
				t.Fatal("ineligible event generated title")
				return "", nil
			}); err != nil {
				t.Fatal(err)
			}
			if p.writes != 0 || p.scans != 0 || titlemark.Open().Load("doubao", "target") != nil {
				t.Fatal("ineligible event mutated/adopted session")
			}
			if which == "disabled" {
				if _, err := os.Stat(StatusPath()); !os.IsNotExist(err) {
					t.Fatal("disabled event wrote status")
				}
			}
		})
	}
}
func TestEventFailureCancellationAndManualRace(t *testing.T) {
	for _, which := range []string{"model", "write", "cancel", "manual race", "missing model"} {
		t.Run(which, func(t *testing.T) {
			p, reg, s := eventFixture(t)
			if which == "missing model" {
				s.TitleModel = nil
			}
			saveEventSettings(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p.fail = which == "write"
			err := onEvent(ctx, reg, "target", func(context.Context, titler.Config, titler.Request) (string, error) {
				switch which {
				case "model":
					return "", errors.New("SECRET model failure")
				case "cancel":
					cancel()
				case "manual race":
					p.conv.Title = "manual"
				}
				return "1109｜Explore｜Conversation Test", nil
			})
			if which == "manual race" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("expected failure")
			}
			if titlemark.Open().Load("doubao", "target") != nil {
				t.Fatal("failed/racing write recorded mark")
			}
			status, statusErr := LoadStatus()
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if which != "manual race" && (status.Failed != 1 || status.Error == "" || status.Renamed != 0) {
				t.Fatalf("status=%+v", status)
			}
			if p.scans != 0 {
				t.Fatal("event scanned")
			}
			data, readErr := os.ReadFile(StatusPath())
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(data), "SECRET") {
				t.Fatal("status leaked upstream contents")
			}
		})
	}
}

func TestEventRefreshKeepsOptionalTitleContract(t *testing.T) {
	p, reg, s := eventFixture(t)
	saveEventSettings(t, s)
	calls := 0
	suggest := func(_ context.Context, _ titler.Config, r titler.Request) (string, error) {
		calls++
		if calls == 1 {
			if !r.RequireTitle {
				t.Fatal("new title is optional")
			}
			return "1109｜Explore｜Conversation Test", nil
		}
		if r.RequireTitle || !r.Refresh {
			t.Fatalf("refresh request=%+v", r)
		}
		return "", nil
	}
	if err := onEvent(context.Background(), reg, "target", suggest); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		p.conv.Messages = append(p.conv.Messages, model.Message{Role: model.RoleUser, Content: "test again"}, model.Message{Role: model.RoleAssistant, Content: "connected"})
	}
	if err := onEvent(context.Background(), reg, "target", suggest); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || p.writes != 1 || p.scans != 0 {
		t.Fatalf("calls=%d writes=%d scans=%d", calls, p.writes, p.scans)
	}
}

func TestLaterEventRetriesModelFailureAndReadsUpdatedConsent(t *testing.T) {
	p, reg, s := eventFixture(t)
	saveEventSettings(t, s)
	calls := 0
	suggest := func(context.Context, titler.Config, titler.Request) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("model failed")
		}
		return "1109｜Explore｜Conversation Test", nil
	}
	if err := onEvent(context.Background(), reg, "target", suggest); err == nil {
		t.Fatal("failure hidden")
	}
	if err := onEvent(context.Background(), reg, "target", suggest); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || p.writes != 1 {
		t.Fatalf("calls=%d writes=%d", calls, p.writes)
	}
	s.TitlePolicy.DoubaoEvents = false
	saveEventSettings(t, s)
	loads := p.loads
	if err := onEvent(context.Background(), reg, "target", suggest); err != nil {
		t.Fatal(err)
	}
	if loads != p.loads || calls != 2 {
		t.Fatal("disabled daemon event touched native data")
	}
}
