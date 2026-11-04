package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// Every delete verb takes -y/--yes, as knife's do, so scripts that pass it
// don't fail with "unknown flag". Walking the live tree means a new delete
// command can't ship without it.
func TestEveryDeleteCommandAcceptsYes(t *testing.T) {
	var deletes []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Name() == "delete" && !c.HasSubCommands() {
			deletes = append(deletes, c)
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(newRootCmd())

	if len(deletes) == 0 {
		t.Fatal("found no delete commands; the walk is broken")
	}
	for _, c := range deletes {
		f := c.Flags().Lookup("yes")
		if f == nil {
			t.Errorf("%q has no --yes flag", c.CommandPath())
			continue
		}
		if f.Shorthand != "y" {
			t.Errorf("%q --yes shorthand = %q, want %q", c.CommandPath(), f.Shorthand, "y")
		}
		if f.Value.Type() != "bool" {
			t.Errorf("%q --yes is a %s, want bool", c.CommandPath(), f.Value.Type())
		}
	}
}
