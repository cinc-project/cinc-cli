package nodeedit

import (
	"errors"
	"testing"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/jsoneditor"
)

// Without a terminal, the node form fails at once with the same error as
// the JSON editor, which points at --file.
func TestRunWithoutTerminalSuggestsFile(t *testing.T) {
	orig := jsoneditor.HaveTerminal
	jsoneditor.HaveTerminal = func() bool { return false }
	t.Cleanup(func() { jsoneditor.HaveTerminal = orig })

	_, _, err := Run(&cinc.Node{Name: "web01", Environment: "_default"})
	if !errors.Is(err, jsoneditor.ErrNoTerminal) {
		t.Fatalf("Run without a terminal = %v, want jsoneditor.ErrNoTerminal", err)
	}
}
