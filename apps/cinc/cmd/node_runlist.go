package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newNodeRunListCmd builds the `cinc node run-list` sub-group: add appends new
// entries, remove drops matching ones, set replaces the whole list, and list
// reads it. The mutators fetch the node, apply the change, and PUT it back;
// knife exposes add/remove/set, and list rounds out the sub-noun so the run
// list is reachable without `node show`.
func newNodeRunListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run-list",
		Short: "List, add, remove, or set a node's run list",
	}
	cmd.AddCommand(newNodeRunListChangeCmd("add"))
	cmd.AddCommand(newNodeRunListChangeCmd("remove"))
	cmd.AddCommand(newNodeRunListChangeCmd("set"))
	cmd.AddCommand(newNodeRunListListCmd())
	return cmd
}

// newNodeRunListListCmd builds `cinc node run-list list <node>`, a read verb
// that prints the node's run list without modifying the server.
func newNodeRunListListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <node>",
		Short: "List a node's run list",
		Example: `Show a node's current run-list.
cinc node run-list list web01`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			node, _, err := c.Nodes.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(node.RunList)
			}
			return printer.New(cmd.OutOrStdout(), format).List(node.RunList)
		},
	}
}

// newNodeRunListChangeCmd builds one of the run-list verbs. Items may be given
// as separate args or comma-separated within an arg (e.g. `recipe[a],role[b]`),
// matching how the other run-list-bearing commands accept them.
func newNodeRunListChangeCmd(verb string) *cobra.Command {
	short := map[string]string{
		"add":    "Append entries to a node's run list",
		"remove": "Remove entries from a node's run list",
		"set":    "Replace a node's run list",
	}[verb]
	example := map[string]string{
		"add": `Append an entry to a node's run-list (existing entries are kept).
cinc node run-list add web01 'recipe[ntp]'`,
		"remove": `Remove an entry from a node's run-list.
cinc node run-list remove web01 'recipe[ntp]'`,
		"set": `Replace a node's run-list entirely.
cinc node run-list set web01 'recipe[base],role[web]'`,
	}[verb]
	return &cobra.Command{
		Use:     verb + " <node> <entry>...",
		Short:   short,
		Example: example,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			items := gatherCSVArgs(args[1:])

			node, _, err := c.Nodes.Get(cmd.Context(), name)
			if err != nil {
				return err
			}
			switch verb {
			case "add":
				node.AddRunListItems(items...)
			case "remove":
				node.RemoveRunListItems(items...)
			case "set":
				node.RunList = items
			}
			node.Name = name
			if _, _, err := c.Nodes.Update(cmd.Context(), node); err != nil {
				return err
			}
			return emitRunList(cmd, format, name, node.RunList)
		},
	}
}

// emitRunList reports a node's run list after a change: the raw slice under
// --format json, or a human line listing the entries.
func emitRunList(cmd *cobra.Command, format printer.Format, name string, runList []string) error {
	if format == printer.FormatJSON {
		return printer.New(cmd.OutOrStdout(), format).Value(runList)
	}
	if len(runList) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Run list for node %q is now empty\n", name)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Run list for node %q is now: %s\n", name, strings.Join(runList, ", "))
	return nil
}

// gatherCSVArgs flattens args that may each be a single entry or a
// comma-separated list of entries into one ordered slice.
func gatherCSVArgs(args []string) []string {
	var out []string
	for _, arg := range args {
		out = append(out, splitCSV(arg)...)
	}
	return out
}
