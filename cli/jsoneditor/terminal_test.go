package jsoneditor

import (
	"errors"
	"strings"
	"testing"
)

// Without a terminal, Run fails at once with an error that points at
// --file, rather than bubbletea's "could not open a new TTY".
func TestRunWithoutTerminalSuggestsFile(t *testing.T) {
	orig := HaveTerminal
	HaveTerminal = func() bool { return false }
	t.Cleanup(func() { HaveTerminal = orig })

	_, err := Run([]byte(`{"a":1}`), func([]byte) error { return nil })
	if !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("Run without a terminal = %v, want ErrNoTerminal", err)
	}
	if !strings.Contains(err.Error(), "--file") {
		t.Errorf("error should suggest --file: %v", err)
	}
}
