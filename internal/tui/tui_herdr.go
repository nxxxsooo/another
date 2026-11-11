package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/herdr"
	"github.com/nxxxsooo/another/internal/provider"
)

type herdrSnapshotMsg struct{ live map[string][]string }
type herdrOpenedMsg struct {
	status string
	err    error
}

// Gate mutations at the common key boundary, before any overlay/shortcut can
// dispatch them. Native Enter also waits: cancelling the probe on quit cannot
// retract an app command already in flight, so exiting to launch another copy
// would race Herdr. Browsing Enter (search/source/preview) remains available.
func (m modelState) herdrConflictingKey(msg tea.KeyMsg) bool {
	if m.searching {
		return false
	}
	key := msg.String()
	switch m.overlay {
	case overlayNone:
		switch key {
		case "a", "u", "r", "ctrl+r", "ctrl+t", "m", "d", "ctrl+d", "right", "enter":
			return true
		}
	case overlayRename, overlayRelocate, overlayTarget, overlayHistory:
		return key == "enter"
	case overlayDelete:
		return key == "enter" && m.deleteChoice != 0
	case overlayBatchTitle:
		return key == "enter" || (!m.batchModelEditing && !m.batchModelPicking && (key == "r" || key == "R"))
	}
	return false
}

func (m modelState) herdrContext() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m modelState) herdrProbeCmd() tea.Cmd {
	client, ctx := m.herdrClient, m.herdrContext()
	return func() tea.Msg {
		// Optional probes keep positively known identities even when unrelated
		// terminals have incomplete metadata. Open rechecks before any action.
		live, _ := client.Snapshot(ctx)
		return herdrSnapshotMsg{live: live}
	}
}

func (m modelState) openInHerdr() (tea.Model, tea.Cmd) {
	if m.herdrOpening {
		return m, nil
	}
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		return m, nil
	}
	if !herdr.Supported(it.summary.Provider) {
		m.err = "Herdr: only local OpenCode sessions are supported"
		return m, nil
	}
	client, ctx, reg, summary := m.herdrClient, m.herdrContext(), m.reg, it.summary
	m.herdrOpening = true
	m.err = ""
	m.status = "Opening in Herdr…"
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		status, err := client.Open(ctx, herdr.Request{
			Provider: summary.Provider, ID: summary.ID, Name: summary.Title, Folder: summary.ProjectPath,
			Resume: func() ([]string, error) {
				p, err := reg.Get(summary.Provider)
				if err != nil {
					return nil, err
				}
				result := provider.WriteResult{SessionID: summary.ID, StoragePath: summary.StoragePath, ProjectPath: summary.ProjectPath}
				if blocker, ok := p.(provider.ResumeBlocker); ok {
					if reason := blocker.ResumeUnavailableReason(result); reason != "" {
						return nil, fmt.Errorf("%s", reason)
					}
				}
				command := p.ResumeCommand(result)
				if command == "" {
					return nil, fmt.Errorf(txt.noResumeCommandFmt, p.DisplayName())
				}
				// ResumeCommand is already shell syntax (including native cd and
				// quoting). Preserve it as one argument; never split/requote it.
				return shellCommand(defaultShell(), command).Args, nil
			},
		})
		return herdrOpenedMsg{status: status, err: err}
	}
}
