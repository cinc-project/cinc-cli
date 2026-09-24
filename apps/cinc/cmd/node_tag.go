package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newNodeTagCmd builds the `cinc node tag` sub-group. Node tags live under the
// node's normal attributes; the cinc-api Node accessors (Tags/AddTags/
// RemoveTags/SetTags) own that storage detail. add, remove, and set mutate the
// tags and PUT the node back, while list reads them without modifying the
// server. knife exposes add/remove/list; set rounds out the sub-noun with a
// wholesale replace.
func newNodeTagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Add, remove, set, or list a node's tags",
	}
	cmd.AddCommand(newNodeTagChangeCmd("add"))
	cmd.AddCommand(newNodeTagChangeCmd("remove"))
	cmd.AddCommand(newNodeTagChangeCmd("set"))
	cmd.AddCommand(newNodeTagListCmd())
	return cmd
}

func newNodeTagChangeCmd(verb string) *cobra.Command {
	short := map[string]string{
		"add":    "Add tags to a node",
		"remove": "Remove tags from a node",
		"set":    "Replace a node's tags",
	}[verb]
	example := map[string]string{
		"add": `Add one or more tags to a node.
cinc node tag add web01 prod canary`,
		"remove": `Remove a tag from a node.
cinc node tag remove web01 canary`,
		"set": `Replace a node's tags entirely.
cinc node tag set web01 prod web`,
	}[verb]
	return &cobra.Command{
		Use:     verb + " <node> <tag>...",
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
			tags := gatherCSVArgs(args[1:])

			node, _, err := c.Nodes.Get(cmd.Context(), name)
			if err != nil {
				return err
			}
			switch verb {
			case "add":
				node.AddTags(tags...)
			case "remove":
				node.RemoveTags(tags...)
			case "set":
				node.SetTags(tags)
			}
			node.Name = name
			if _, _, err := c.Nodes.Update(cmd.Context(), node); err != nil {
				return err
			}
			return emitNodeTags(cmd, format, name, node.Tags())
		},
	}
}

func newNodeTagListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <node>",
		Short: "List a node's tags",
		Example: `Show a node's current tags.
cinc node tag list web01`,
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
			tags := node.Tags()
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(tags)
			}
			return printer.New(cmd.OutOrStdout(), format).List(tags)
		},
	}
}

// emitNodeTags reports a node's tags after a change: the raw slice under
// --format json, or a human line.
func emitNodeTags(cmd *cobra.Command, format printer.Format, name string, tags []string) error {
	if format == printer.FormatJSON {
		return printer.New(cmd.OutOrStdout(), format).Value(tags)
	}
	if len(tags) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Node %q now has no tags\n", name)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Node %q tags are now: %s\n", name, strings.Join(tags, ", "))
	return nil
}
