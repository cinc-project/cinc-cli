package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestCreateExamplesMatchWhatCreateDoes guards the help of the create
// commands that never open an editor: they build the object from the name,
// --description, or --file, so their examples must not promise an editor and
// should show the --file path that defines run lists and attributes.
func TestCreateExamplesMatchWhatCreateDoes(t *testing.T) {
	for name, build := range map[string]func() *cobra.Command{
		"role create":        newRoleCreateCmd,
		"environment create": newEnvironmentCreateCmd,
	} {
		t.Run(name, func(t *testing.T) {
			example := build().Example
			if strings.Contains(strings.ToLower(example), "editor") {
				t.Errorf("%s example mentions an editor, but the command doesn't open one:\n%s", name, example)
			}
			if !strings.Contains(example, "--file") {
				t.Errorf("%s example should show --file:\n%s", name, example)
			}
		})
	}
}
