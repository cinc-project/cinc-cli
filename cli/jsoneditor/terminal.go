package jsoneditor

import (
	"errors"
	"os"
	"runtime"

	"golang.org/x/term"
)

// ErrNoTerminal is returned by Run (and the node-edit form) when there is
// no terminal to draw the editor on, as in a script or a CI job. Every
// command that opens the editor also takes --file, so the error says so.
var ErrNoTerminal = errors.New("we couldn't open the editor because there's no terminal here. " +
	"Pass --file with the updated JSON instead")

// HaveTerminal reports whether an interactive editor can run: stdin is a
// terminal, or the process has a controlling terminal it can open, which
// is where bubbletea reads keys from when stdin is redirected. It is a
// package variable so tests can pretend either way.
var HaveTerminal = func() bool {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return true
	}
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONIN$"
	}
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
