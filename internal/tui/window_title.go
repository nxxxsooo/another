package tui

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/nxxxsooo/another/internal/util"
)

// windowTitle is what the terminal should read while another owns the screen.
// Without it the tab keeps whatever the shell last set — usually the working
// directory, which says nothing about the program actually running there.
const windowTitle = "another"

// setupWindowTitle marks the one screen that is not the browser, so a setup run
// left open in a background tab is still identifiable.
const setupWindowTitle = "another setup"

// setWindowTitle writes the title change itself, for the moments no bubbletea
// program is running: the terminal keeps another's title after the program
// exits unless someone puts something else there. It is a no-op off a terminal
// so piped output never carries an escape sequence.
func setWindowTitle(w io.Writer, title string) {
	if title == "" || !term.IsTerminal(os.Stdout.Fd()) {
		return
	}
	fmt.Fprintf(w, "\x1b]2;%s\a", title)
}

// restoreWindowTitle puts back the working directory a shell normally shows, so
// a terminal without shell integration is not left reading "another" long after
// the program exited.
func restoreWindowTitle(w io.Writer) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	setWindowTitle(w, util.TildePath(cwd))
}
