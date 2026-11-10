package tui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/model"
)

func TestNativeProjectLabelInRenderedSessionRow(t *testing.T) {
	row := sessionItem{summary: model.Summary{
		ID: "one", Provider: "codex", Title: "Discuss a short film", ProjectLabel: "影像工坊",
		ProjectPath: filepath.Join(t.TempDir(), ".chatgpt-projects", "g-p-example"),
	}}
	d := sessionDelegate{global: true, showProject: true, pathDepth: pathTailDepth}
	for _, width := range []int{60, 80, 120, 160} {
		m := list.New([]list.Item{row}, d, width, 20)
		var rendered bytes.Buffer
		d.Render(&rendered, m, 0, row)
		text := ansi.Strip(rendered.String())
		if ansi.StringWidth(text) > width || strings.Contains(text, "g-p-example") {
			t.Errorf("row at width %d = %q", width, text)
		}
		if width >= 120 && !strings.Contains(text, row.summary.ProjectLabel) {
			t.Errorf("row at width %d lost native label: %q", width, text)
		}
	}
}

func TestNativeProjectLabelRendersAsTextAndRetainsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".chatgpt-projects", "g-p-example")
	row := sessionItem{summary: model.Summary{ID: "one", ProjectPath: path, ProjectLabel: "Studio / films / 2026 / ideas"}}
	d := sessionDelegate{global: true, pathDepth: 1}
	cell := ansi.Strip(d.renderSessionProjectCell(row, 40))
	if !strings.Contains(cell, row.summary.ProjectLabel) || strings.Contains(cell, "g-p-example") {
		t.Fatalf("label was shortened as a path: %q", cell)
	}
	if !strings.Contains(row.FilterValue(), path) || !strings.Contains(row.FilterValue(), row.summary.ProjectLabel) {
		t.Fatal("filter lost path or native label")
	}
	for _, width := range []int{0, 1, 3, 8, 18} {
		if cell := d.renderSessionProjectCell(row, width); ansi.StringWidth(cell) > width {
			t.Errorf("label cell exceeds width %d: %q", width, cell)
		}
	}
	row.summary.ProjectLabel = ""
	d.pathDepth = pathTailDepth
	if got, want := d.renderSessionProjectCell(row, 40), renderProjectCellState(path, "", 40, false); got != want {
		t.Fatalf("missing-label fallback = %q, want %q", got, want)
	}
}

func TestNativeProjectGroupLabelDoesNotChangeIdentity(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "g-p-first")
	second := filepath.Join(root, "g-p-second")
	items := []list.Item{
		sessionItem{summary: model.Summary{ID: "one", ProjectPath: first, ProjectLabel: "Same studio"}},
		sessionItem{summary: model.Summary{ID: "two", ProjectPath: second, ProjectLabel: "Same studio"}},
	}
	grouped := groupSessions(items, nil, "")
	if len(grouped) != 4 {
		t.Fatalf("same-name projects merged: %+v", grouped)
	}
	d := sessionDelegate{global: true, bands: true, pathDepth: 1}
	for i, path := range []string{first, second} {
		head := grouped[i*2].(groupHeader)
		if head.path != path || head.foldKey() != path {
			t.Fatalf("label replaced group identity: %+v", head)
		}
		if got := ansi.Strip(d.renderGroupHeader(head, 100, false)); !strings.Contains(got, "Same studio") || strings.Contains(got, "g-p-") {
			t.Fatalf("group label = %q", got)
		}
		if got := strings.TrimSpace(ansi.Strip(d.renderSessionProjectCell(items[i].(sessionItem), 30))); got != "" {
			t.Fatalf("grouped row repeated its project: %q", got)
		}
	}
}

func TestConflictingNativeProjectGroupLabelsFallBackToPath(t *testing.T) {
	first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	items := []list.Item{
		sessionItem{summary: model.Summary{ProjectPath: first, ProjectLabel: "First name"}},
		sessionItem{summary: model.Summary{ProjectPath: first, ProjectLabel: "Other name"}},
		sessionItem{summary: model.Summary{ProjectPath: first, ProjectLabel: "First name"}},
		sessionItem{summary: model.Summary{ProjectPath: second}},
	}
	head := groupSessions(items, nil, "")[0].(groupHeader)
	if head.projectLabel != "" || head.path != first {
		t.Fatalf("ambiguous labels picked a winner: %+v", head)
	}
}
