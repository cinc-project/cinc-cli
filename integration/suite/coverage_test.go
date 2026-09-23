package suite

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/apps/cinc/cmd"
)

// TestCoverage is the durable coverage guard. It walks the live cobra tree and
// fails when a shipped leaf command is neither covered by a suite case, nor
// exempt with a reason, nor pending in its family; or when any of those lists
// names a command that does not exist. It needs no server.
func TestCoverage(t *testing.T) {
	leaves := liveLeafCommands()
	accounted := map[string]string{} // leaf -> where it is accounted for

	claim := func(path, where string) {
		if !slices.Contains(leaves, path) {
			t.Errorf("%s names %q, which is not a leaf command", where, path)
			return
		}
		if prev, ok := accounted[path]; ok && !strings.HasPrefix(prev, "case ") {
			t.Errorf("%q is listed by both %s and %s", path, prev, where)
		}
		if _, ok := accounted[path]; !ok {
			accounted[path] = where
		}
	}
	for _, f := range families() {
		for _, tc := range f.cases {
			if len(tc.covers) == 0 {
				t.Errorf("case %q covers no command", tc.name)
			}
			for _, p := range tc.covers {
				claim(p, "case "+tc.name)
			}
		}
	}
	for _, f := range families() {
		for _, p := range f.pending {
			if where, ok := accounted[p]; ok {
				t.Errorf("%q is pending but already covered by %s; drop it from pending", p, where)
				continue
			}
			claim(p, "pending")
		}
	}
	for _, p := range slices.Sorted(maps.Keys(exempt)) {
		if strings.TrimSpace(exempt[p]) == "" {
			t.Errorf("exempt %q has no reason", p)
		}
		if where, ok := accounted[p]; ok {
			t.Errorf("%q is exempt but also %s", p, where)
			continue
		}
		claim(p, "exempt")
	}
	for _, leaf := range leaves {
		if _, ok := accounted[leaf]; !ok {
			t.Errorf("leaf command %q has no suite case; add one (or an exempt entry with a reason)", leaf)
		}
	}
}

// liveLeafCommands walks cmd.NewRootCmd() and returns every leaf command path
// (space-joined, without the root), skipping cobra's help and completion.
func liveLeafCommands() []string {
	var leaves []string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		var children []*cobra.Command
		for _, ch := range c.Commands() {
			if ch.Name() == "help" || ch.Name() == "completion" {
				continue
			}
			children = append(children, ch)
		}
		if len(children) == 0 {
			if len(path) > 0 {
				leaves = append(leaves, strings.Join(path, " "))
			}
			return
		}
		for _, ch := range children {
			walk(ch, append(append([]string{}, path...), ch.Name()))
		}
	}
	walk(cmd.NewRootCmd(), nil)
	slices.Sort(leaves)
	return leaves
}
