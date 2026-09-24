package cmd

import (
	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"
)

// newNodeTagCmd builds the `cinc node tag` sub-group. Node tags live under the
// node's normal attributes; the cinc-api Node accessors (Tags/AddTags/
// RemoveTags/SetTags) own that storage detail. knife exposes add/remove/list;
// set rounds out the sub-noun with a wholesale replace.
func newNodeTagCmd() *cobra.Command {
	return newNodeListFieldCmd(nodeListField{
		use:   "tag",
		short: "Add, remove, set, or list a node's tags",
		entry: "<tag>",
		get:   (*cinc.Node).Tags,
		change: map[string]func(*cinc.Node, []string){
			"add":    func(n *cinc.Node, tags []string) { n.AddTags(tags...) },
			"remove": func(n *cinc.Node, tags []string) { n.RemoveTags(tags...) },
			"set":    (*cinc.Node).SetTags,
		},
		shorts: map[string]string{
			"add":    "Add tags to a node",
			"remove": "Remove tags from a node",
			"set":    "Replace a node's tags",
			"list":   "List a node's tags",
		},
		examples: map[string]string{
			"add": `Add one or more tags to a node.
cinc node tag add web01 prod canary`,
			"remove": `Remove a tag from a node.
cinc node tag remove web01 canary`,
			"set": `Replace a node's tags entirely.
cinc node tag set web01 prod web`,
			"list": `Show a node's current tags.
cinc node tag list web01`,
		},
		now:       "Node %q tags are now: %s\n",
		nowEmpty:  "Node %q now has no tags\n",
		same:      "Node %q already has those tags, so there's nothing to change: %s\n",
		sameEmpty: "Node %q has no tags, so there's nothing to change\n",
	})
}
