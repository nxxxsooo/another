package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
	"github.com/nxxxsooo/another/internal/util"
)

// maxShowAllPage caps one fetch. The list scrolls, so there is no user-facing
// pagination; this only keeps a pathological index from being loaded whole.
const maxShowAllPage = 200

// Overlays are transient panels drawn on top of the session list. They are not
// stages: closing one returns to exactly the row the user was on.
const (
	overlayNone = iota
	overlaySource
	overlayTarget
	overlayPreview
	overlayDelete
	overlayRename
	overlayRelocate
	overlayBatchTitle
)

type modelState struct {
	reg    *registry.Registry
	idx    *index.Store
	engine *migrate.Engine

	sessions      list.Model
	sourceList    list.Model
	targets       list.Model
	preview       viewport.Model
	searchInput   textinput.Model
	renameInput   textinput.Model
	relocateInput textinput.Model
	spinner       spinner.Model

	sources      []sourceChip
	sourceIdx    int
	overlay      int
	deleteChoice int // 0 cancel, 1 delete

	// relocateMove selects move over the default fork. It resets every time
	// the overlay opens: carrying a session out of its directory is the
	// destructive reading of this action and never becomes the sticky one.
	relocateMove bool
	// relocateCanMove records whether the selected provider owns a native
	// move, so the toggle is not offered where it cannot be honoured.
	relocateCanMove bool

	// titleCfg is empty unless setup picked an agent to write suggestions.
	titleCfg   titler.Config
	suggesting bool
	suggestion string
	suggestErr string
	suggestFor string

	// marked holds the session IDs chosen for a batch action, keyed by ID
	// rather than list index: filtering and page reloads rebuild the rows, and
	// an index-keyed selection would silently follow a different session.
	marked map[string]bool

	// batch* carries the bulk-rename flow. Items keep mark order, results
	// stream in out of order, and total counts both from the moment the
	// engine starts. The channel and cancel func are nil outside a run.
	batchItems      []model.Summary
	batchByID       map[string]model.Summary
	batchMissing    []titler.BatchResult
	batchResults    []titler.BatchResult
	batchTotal      int
	batchCh         <-chan titler.BatchResult
	batchCancel     context.CancelFunc
	batchRunning    bool
	batchCancelling bool
	batchExpanded   bool
	// batchCfg is titleCfg plus any model chosen for this batch alone. The
	// override is deliberately not persisted: a cheap model for forty old
	// sessions should not become the default for the next single rename.
	batchCfg          titler.Config
	batchModelInput   textinput.Model
	batchModelEditing bool
	// batchModel* is the same picker setup uses, pulled from the agent's own
	// CLI so a temporary override cannot be a name that CLI would reject.
	batchModelPicking bool
	batchModelLoading bool
	batchModelOpts    []string
	batchModelCursor  int
	batchModelFilter  string
	batchModelErr     string
	// batchGen orphans a superseded run. Re-running on a different model
	// leaves the previous engine draining in the background, and its results
	// must not land in the new list.
	batchGen uint64

	selected        *sessionItem
	loading         bool
	indexing        bool
	contentIndexing bool
	searching       bool
	searchQuery     string
	totalSessions   int
	cwd             string
	movedAway       []string
	projectScope    util.ProjectScope
	projectOnly     bool
	sessionSpacing  int
	pageGen         uint64
	lastResume      string
	lastArchived    *model.Summary
	// lastDeleted and restoreDeleted are the one-step undo for a delete. They
	// live only as long as this list does, and only for providers that can put
	// the very same session back.
	lastDeleted    *model.Summary
	restoreDeleted provider.SessionRestore
	contextMode    migrate.ContextMode
	err            string
	status         string
	// launch is the resume command the caller should exec after the program
	// exits. Running it from inside bubbletea would fight over the terminal.
	launch         string
	launchTarget   string
	launchProject  string
	width          int
	height         int
	ctx            context.Context
	cancel         context.CancelFunc
	previewContent string
}

func Run(reg *registry.Registry, idx *index.Store, engine *migrate.Engine, contextMode migrate.ContextMode) error {
	return run(reg, idx, engine, nil, contextMode)
}

// RunMigrate opens the picker with a session preselected and its target overlay
// already up.
func RunMigrate(reg *registry.Registry, idx *index.Store, engine *migrate.Engine, sessionID, from string, contextMode migrate.ContextMode) error {
	var selected *model.Summary
	if sessionID != "" {
		sm, _, err := migrate.ResolveSession(context.Background(), reg, idx, sessionID, from)
		if err != nil {
			return err
		}
		selected = sm
	}
	return run(reg, idx, engine, selected, contextMode)
}

func run(reg *registry.Registry, idx *index.Store, engine *migrate.Engine, initial *model.Summary, contextMode migrate.ContextMode) error {
	cwd, err := os.Getwd()
	if err == nil {
		cwd = util.NormalizeProjectPath(cwd)
	}
	projectScope := util.DiscoverProjectScope(context.Background(), cwd)
	resolveNestedRepos(idx, &projectScope)
	initialOpts := index.ListOpts{IncludeSubagents: false}
	applyProjectScope(&initialOpts, projectScope)
	counts, _ := idx.CountByProviderFiltered(initialOpts)

	// One map instance is shared with the delegate; see sessionDelegate.
	marked := map[string]bool{}
	sessList := newSessionList(nil, marked)
	sources := sourceChips(reg, counts)
	sourceList := newSourceList(sourceItems(sources))
	targetList := newTargetList(nil)

	vp := viewport.New(64, 20)
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "search titles and messages"
	rename := textinput.New()
	rename.Prompt = ""
	rename.Placeholder = txt.renamePlaceholder
	rename.CharLimit = 200
	relocate := textinput.New()
	relocate.Prompt = ""
	relocate.Placeholder = txt.relocatePlaceholder
	relocate.CharLimit = 1024
	sp := newWaitSpinner()

	// Settings are read here rather than threaded through every caller: the
	// suggestion agent is a TUI-only concern and an unreadable config simply
	// leaves the feature off.
	var titleCfg titler.Config
	if settings, err := config.LoadSettings(); err == nil && settings.TitleModel != nil {
		titleCfg = titler.Config{
			Provider: settings.TitleModel.Provider,
			Model:    settings.TitleModel.Model,
			Language: titler.NormalizeLanguage(titler.Language(settings.TitleModel.Language)),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := modelState{
		reg: reg, idx: idx, engine: engine,
		titleCfg: titleCfg,
		marked:   marked,
		sessions: sessList, sourceList: sourceList, targets: targetList,
		preview: vp, searchInput: search, renameInput: rename, relocateInput: relocate, spinner: sp,
		sources: sources, cwd: cwd, projectScope: projectScope, projectOnly: cwd != "",
		indexing: index.NeedsIncrementalIndex(reg, idx, 5*time.Minute), pageGen: 1,
		ctx: ctx, cancel: cancel, contextMode: contextMode,
		movedAway: movedAwayDirectories(idx, initialOpts.ProjectRoots),
	}
	if err != nil {
		m.err = txt.cwdUnreadable + err.Error()
	}
	m.contentIndexing = !m.indexing
	if initial != nil {
		sel := sessionItem{summary: *initial}
		m.selected = &sel
		m.sessions.SetItems([]list.Item{sel})
		m.targets.SetItems(targetItems(reg, initial.Provider))
		m.overlay = overlayTarget
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	restoreInputSource := temporarilyUseASCIIInputSource()
	defer restoreInputSource()
	final, runErr := p.Run()
	restoreInputSource()
	if runErr != nil {
		return runErr
	}
	done, ok := final.(modelState)
	if ok && done.launch != "" {
		// Handing the terminal to another agent: no goodbye screen, or it
		// lands as noise right before that agent paints its own startup. The
		// title goes with the terminal, so it names the agent taking over
		// rather than staying on another for that agent's whole session.
		setWindowTitle(os.Stdout, registry.DisplayName(reg, done.launchTarget))
		return launchResume(done.launch, done.launchTarget, done.launchProject)
	}
	restoreWindowTitle(os.Stdout)
	if ok {
		playFarewell(os.Stdout, done.width, done.height, true)
	}
	return nil
}

// launchResume replaces this process with the target agent. Claude Code is the
// one exception: accepting a project's first trust prompt records the decision
// and exits instead of resuming. For that exact transition we run it once more;
// a normal Claude exit never restarts.
func launchResume(command, target, project string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	fmt.Fprintln(os.Stderr, command)
	if target != "claude-code" {
		return syscall.Exec(shell, []string{shell, "-c", command}, os.Environ())
	}

	configPath := filepath.Join(config.HomeDir(), ".claude.json")
	trustedBefore := claudeProjectTrusted(configPath, project)
	cmd := exec.Command(shell, "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if trustedBefore || !claudeProjectTrusted(configPath, project) {
		return nil
	}
	fmt.Fprintln(os.Stderr, "Workspace trusted; resuming Claude Code…")
	return syscall.Exec(shell, []string{shell, "-c", command}, os.Environ())
}

func claudeProjectTrusted(configPath, project string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	var state struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(data, &state) != nil {
		return false
	}
	if state.Projects[project].Trusted {
		return true
	}
	return state.Projects[util.NormalizeProjectPath(project)].Trusted
}

func (m modelState) sourceID() string {
	if m.sourceIdx < 0 || m.sourceIdx >= len(m.sources) {
		return ""
	}
	return m.sources[m.sourceIdx].id
}

func (m modelState) currentSource() sourceChip {
	if m.sourceIdx < 0 || m.sourceIdx >= len(m.sources) {
		return sourceChip{name: "all", count: m.totalSessions}
	}
	return m.sources[m.sourceIdx]
}

func (m *modelState) updateSourceCounts(counts map[string]int) {
	selectedID := m.sourceID()
	m.sources = sourceChips(m.reg, counts)
	m.sourceIdx = 0
	for i := range m.sources {
		if m.sources[i].id == selectedID {
			m.sourceIdx = i
			break
		}
	}
	m.sourceList.SetItems(sourceItems(m.sources))
	m.sourceList.Select(m.sourceIdx)
}

func (m modelState) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.HideCursor, tea.SetWindowTitle(windowTitle), m.spinner.Tick, probeSizeCmd(0), loadSessionsPageCmd(m, m.pageGen)}
	if m.indexing {
		cmds = append(cmds, backgroundIndexCmd(m.ctx, m.reg, m.idx))
	} else {
		cmds = append(cmds, contentIndexCmd(m.ctx, m.reg, m.idx))
	}
	return tea.Batch(cmds...)
}

// newWaitSpinner builds the one animation another waits with, so every wait
// looks the same wherever it is drawn. OpenCode 2's compact braille spinner
// stays one cell wide, so the progress counter and modal never shift between
// animation frames.
func newWaitSpinner() spinner.Model {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = accentStyle
	return sp
}
