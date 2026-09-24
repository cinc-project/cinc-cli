package cmd

import (
	"fmt"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// nodeListField is a list of strings on a node that a `cinc node <noun>`
// sub-group manages with add, remove, set, and list: the run list or the
// tags. The mutators go through Nodes.Modify, so a change that leaves the
// list as it was sends no PUT, and what gets reported is the node the server
// answered with.
type nodeListField struct {
	use, short string
	// entry is the positional placeholder for one list entry, e.g. "<tag>".
	entry string
	get   func(*cinc.Node) []string
	// change applies each mutating verb (add, remove, set) to a node.
	change map[string]func(n *cinc.Node, entries []string)
	// shorts and examples hold each verb's help, list included.
	shorts, examples map[string]string
	// now and same report the list after a change and after a no-op. Each
	// takes the node name and the comma-joined list; the *Empty forms take
	// just the name, for an empty list.
	now, nowEmpty, same, sameEmpty string
}

// newNodeRunListCmd builds the `cinc node run-list` sub-group: add appends new
// entries, remove drops matching ones, set replaces the whole list, and list
// reads it. knife exposes add/remove/set, and list rounds out the sub-noun so
// the run list is reachable without `node show`. Entries are normalized the
// way the Chef Server stores them, so a bare "nginx" means "recipe[nginx]".
func newNodeRunListCmd() *cobra.Command {
	return newNodeListFieldCmd(nodeListField{
		use:   "run-list",
		short: "List, add, remove, or set a node's run list",
		entry: "<entry>",
		get:   func(n *cinc.Node) []string { return n.RunList },
		change: map[string]func(*cinc.Node, []string){
			"add":    func(n *cinc.Node, items []string) { n.AddRunListItems(items...) },
			"remove": func(n *cinc.Node, items []string) { n.RemoveRunListItems(items...) },
			"set":    func(n *cinc.Node, items []string) { n.RunList = cinc.NormalizeRunList(items) },
		},
		shorts: map[string]string{
			"add":    "Append entries to a node's run list",
			"remove": "Remove entries from a node's run list",
			"set":    "Replace a node's run list",
			"list":   "List a node's run list",
		},
		examples: map[string]string{
			"add": `Append an entry to a node's run-list (existing entries are kept).
cinc node run-list add web01 'recipe[ntp]'`,
			"remove": `Remove an entry from a node's run-list.
cinc node run-list remove web01 'recipe[ntp]'`,
			"set": `Replace a node's run-list entirely.
cinc node run-list set web01 'recipe[base],role[web]'`,
			"list": `Show a node's current run-list.
cinc node run-list list web01`,
		},
		now:       "Run list for node %q is now: %s\n",
		nowEmpty:  "Run list for node %q is now empty\n",
		same:      "Node %q already has that run list, so there's nothing to change: %s\n",
		sameEmpty: "Node %q already has an empty run list, so there's nothing to change\n",
	})
}

// newNodeListFieldCmd builds the sub-group for one nodeListField. Entries may be
// given as separate args or comma-separated within an arg (e.g.
// `recipe[a],role[b]`), matching how the other run-list-bearing commands
// accept them.
func newNodeListFieldCmd(f nodeListField) *cobra.Command {
	group := &cobra.Command{Use: f.use, Short: f.short}
	for _, verb := range []string{"add", "remove", "set"} {
		group.AddCommand(&cobra.Command{
			Use:     verb + " <node> " + f.entry + "...",
			Short:   f.shorts[verb],
			Example: f.examples[verb],
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
				name, entries := args[0], gatherCSVArgs(args[1:])
				node, changed, err := c.Nodes.Modify(cmd.Context(), name, func(n *cinc.Node) error {
					f.change[verb](n, entries)
					return nil
				})
				if err != nil {
					return err
				}
				return f.emit(cmd, format, name, f.get(node), changed)
			},
		})
	}
	group.AddCommand(&cobra.Command{
		Use:     "list <node>",
		Short:   f.shorts["list"],
		Example: f.examples["list"],
		Args:    cobra.ExactArgs(1),
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
			entries := f.get(node)
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(nonNilStrings(entries))
			}
			return printer.New(cmd.OutOrStdout(), format).List(entries)
		},
	})
	return group
}

// emit reports the list after a change: the bare array under --format json,
// or a line saying what the list is now, or that it already was.
func (f nodeListField) emit(cmd *cobra.Command, format printer.Format, name string, entries []string, changed bool) error {
	out := cmd.OutOrStdout()
	if format == printer.FormatJSON {
		return printer.New(out, format).Value(nonNilStrings(entries))
	}
	msg, empty := f.now, f.nowEmpty
	if !changed {
		msg, empty = f.same, f.sameEmpty
	}
	if len(entries) == 0 {
		fmt.Fprintf(out, empty, name)
		return nil
	}
	fmt.Fprintf(out, msg, name, strings.Join(entries, ", "))
	return nil
}

// nonNilStrings returns s, or an empty slice for nil, so --format json
// prints [] rather than null.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
