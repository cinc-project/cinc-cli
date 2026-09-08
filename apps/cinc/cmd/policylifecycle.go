package cmd

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// policyfileTemplate is the scaffold written by `cinc policy create`. The
// single %[1]s is the policy name; it fills the filename comment, the `name`
// directive, the run_list, and the example cookbook line so they all agree.
const policyfileTemplate = `# %[1]s.rb - Describe how you want Cinc Infra Client to build your system.
#
# For more information on the Policyfile feature, see the Policyfile
# documentation: https://docs.chef.io/policyfile/   (Cinc is Policyfile-compatible)

# A name that describes what the system you're building with Cinc does.
name '%[1]s'

# Where to find external cookbooks:
default_source :supermarket

# run_list: Cinc Infra Client will run these recipes in the order specified.
run_list '%[1]s::default'

# Specify a custom source for a single cookbook:
# cookbook '%[1]s', path: '.'
`

// newPolicyCreateCmd builds `cinc policy create <name>`. Unlike the other
// `create` verbs this one is local: it scaffolds a Policyfile authoring
// source on disk rather than hitting the server.
func newPolicyCreateCmd() *cobra.Command {
	var outFile string
	var force bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a new Policyfile on disk",
		Example: `Scaffold a starter Policyfile.rb on disk.
cinc policy create base`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := outFile
			if path == "" {
				path = name + ".rb"
			}
			if !force {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("%s already exists (use --force to overwrite)", path)
				}
			}
			body := fmt.Sprintf(policyfileTemplate, name)
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return fmt.Errorf("cinc: write %s: %w", path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created Policyfile %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&outFile, "file", "", "write the Policyfile to this path instead of ./<name>.rb")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the file if it already exists")
	return cmd
}

// policyDiffSide names one side of a diff: the ref the user gave (a group
// name or a revision id) and the revision id it resolved to.
type policyDiffSide struct {
	Ref        string `json:"ref"`
	RevisionID string `json:"revision_id"`
}

// cookbookDelta is one cookbook's change between two revisions. An empty
// From means the cookbook was added; an empty To means it was removed.
type cookbookDelta struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// runListDelta is the set difference of two run lists.
type runListDelta struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// attrDelta is one changed attribute leaf, keyed by its flattened path.
type attrDelta struct {
	Path string `json:"path"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
}

// policyDiff is the structured delta between two policy revisions.
type policyDiff struct {
	Policy     string          `json:"policy"`
	From       policyDiffSide  `json:"from"`
	To         policyDiffSide  `json:"to"`
	Cookbooks  []cookbookDelta `json:"cookbooks"`
	RunList    runListDelta    `json:"run_list"`
	Attributes []attrDelta     `json:"attributes"`
}

// empty reports whether the two revisions were identical in every dimension
// the diff considers.
func (d policyDiff) empty() bool {
	return len(d.Cookbooks) == 0 && len(d.RunList.Added) == 0 &&
		len(d.RunList.Removed) == 0 && len(d.Attributes) == 0
}

// computePolicyDiff is the pure comparison of two revisions. fromRef/toRef are
// the user-facing labels (group names or revision ids); the revision ids come
// from the fetched documents.
func computePolicyDiff(policy, fromRef, toRef string, a, b *cinc.PolicyRevision) policyDiff {
	return policyDiff{
		Policy:     policy,
		From:       policyDiffSide{Ref: fromRef, RevisionID: a.RevisionID},
		To:         policyDiffSide{Ref: toRef, RevisionID: b.RevisionID},
		Cookbooks:  diffCookbooks(a.CookbookLocks, b.CookbookLocks),
		RunList:    diffRunList(a.RunList, b.RunList),
		Attributes: diffAttributes(a, b),
	}
}

// cookbookRefs returns the value to show for a lock when versions differ
// (the version) versus when only the identifier differs (the identifier).
func diffCookbooks(a, b map[string]cinc.CookbookLock) []cookbookDelta {
	names := map[string]struct{}{}
	for n := range a {
		names[n] = struct{}{}
	}
	for n := range b {
		names[n] = struct{}{}
	}
	var deltas []cookbookDelta
	for n := range names {
		la, okA := a[n]
		lb, okB := b[n]
		switch {
		case okA && !okB:
			deltas = append(deltas, cookbookDelta{Name: n, From: lockRef(la)})
		case !okA && okB:
			deltas = append(deltas, cookbookDelta{Name: n, To: lockRef(lb)})
		case la.Version != lb.Version:
			deltas = append(deltas, cookbookDelta{Name: n, From: la.Version, To: lb.Version})
		case la.Identifier != lb.Identifier:
			deltas = append(deltas, cookbookDelta{Name: n, From: la.Identifier, To: lb.Identifier})
		}
	}
	slices.SortFunc(deltas, func(x, y cookbookDelta) int { return cmpString(x.Name, y.Name) })
	return deltas
}

// lockRef prefers the human-friendly version, falling back to the
// content identifier when a lock carries no version.
func lockRef(l cinc.CookbookLock) string {
	if l.Version != "" {
		return l.Version
	}
	return l.Identifier
}

func diffRunList(a, b []string) runListDelta {
	d := runListDelta{Added: []string{}, Removed: []string{}}
	inA := map[string]struct{}{}
	for _, r := range a {
		inA[r] = struct{}{}
	}
	inB := map[string]struct{}{}
	for _, r := range b {
		inB[r] = struct{}{}
	}
	for _, r := range b {
		if _, ok := inA[r]; !ok {
			d.Added = append(d.Added, r)
		}
	}
	for _, r := range a {
		if _, ok := inB[r]; !ok {
			d.Removed = append(d.Removed, r)
		}
	}
	return d
}

// diffAttributes flattens default_ and override_attributes to leaf paths and
// reports every leaf that was added, removed, or changed.
func diffAttributes(a, b *cinc.PolicyRevision) []attrDelta {
	flatA := map[string]any{}
	flatB := map[string]any{}
	flattenAttrs("default", a.DefaultAttributes, flatA)
	flattenAttrs("override", a.OverrideAttributes, flatA)
	flattenAttrs("default", b.DefaultAttributes, flatB)
	flattenAttrs("override", b.OverrideAttributes, flatB)

	paths := map[string]struct{}{}
	for p := range flatA {
		paths[p] = struct{}{}
	}
	for p := range flatB {
		paths[p] = struct{}{}
	}
	var deltas []attrDelta
	for p := range paths {
		va, okA := flatA[p]
		vb, okB := flatB[p]
		if okA && okB && reflect.DeepEqual(va, vb) {
			continue
		}
		d := attrDelta{Path: p}
		if okA {
			d.From = va
		}
		if okB {
			d.To = vb
		}
		deltas = append(deltas, d)
	}
	slices.SortFunc(deltas, func(x, y attrDelta) int { return cmpString(x.Path, y.Path) })
	return deltas
}

// flattenAttrs walks a nested attribute map, emitting one entry per leaf keyed
// by a knife-style path like default['web']['port'].
func flattenAttrs(prefix string, v map[string]any, out map[string]any) {
	for k, val := range v {
		path := prefix + "['" + k + "']"
		if m, ok := val.(map[string]any); ok {
			flattenAttrs(path, m, out)
			continue
		}
		out[path] = val
	}
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// newPolicyDiffCmd builds `cinc policy diff <name> ...`. With --revisions it
// compares two revision ids; otherwise it compares the revision active in two
// policy groups.
func newPolicyDiffCmd() *cobra.Command {
	var revisionsForm bool
	cmd := &cobra.Command{
		Use:   "diff <name> <ref1> <ref2>",
		Short: "Compare two revisions of a policy",
		Example: `Compare two revisions of a policy.
cinc policy diff appserver 1.0.0 1.1.0`,
		Long: "Compare two revisions of a policy.\n\n" +
			"By default ref1 and ref2 name policy groups and the comparison is\n" +
			"between the revision active in each. Pass --revisions to treat them as\n" +
			"revision ids instead: cinc policy diff NAME --revisions A B.",
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			refs := args[1:]
			if len(refs) != 2 {
				if revisionsForm {
					return fmt.Errorf("--revisions takes exactly two revision ids: cinc policy diff %s --revisions A B", name)
				}
				return fmt.Errorf("give two policy group names: cinc policy diff %s GROUP1 GROUP2 (or use --revisions A B)", name)
			}
			fromRef, toRef := refs[0], refs[1]

			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}

			fromRev, toRev := fromRef, toRef
			if !revisionsForm {
				if fromRev, err = activeRevision(cmd.Context(), c, fromRef, name); err != nil {
					return err
				}
				if toRev, err = activeRevision(cmd.Context(), c, toRef, name); err != nil {
					return err
				}
			}

			a, _, err := c.Policies.GetRevision(cmd.Context(), name, fromRev)
			if err != nil {
				return err
			}
			b, _, err := c.Policies.GetRevision(cmd.Context(), name, toRev)
			if err != nil {
				return err
			}

			d := computePolicyDiff(name, fromRef, toRef, a, b)
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(d)
			}
			renderPolicyDiff(cmd, d)
			return nil
		},
	}
	cmd.Flags().BoolVar(&revisionsForm, "revisions", false, "treat the two refs as revision ids rather than policy group names")
	return cmd
}

// activeRevision returns the revision id of policy active in group.
func activeRevision(ctx context.Context, c *cinc.Client, group, policy string) (string, error) {
	g, _, err := c.PolicyGroups.Get(ctx, group)
	if err != nil {
		return "", err
	}
	assignment, ok := g.Policies[policy]
	if !ok {
		return "", fmt.Errorf("policy %q is not assigned to group %q", policy, group)
	}
	return assignment.RevisionID, nil
}

// renderPolicyDiff prints the human form of a diff.
func renderPolicyDiff(cmd *cobra.Command, d policyDiff) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s: %s (%s) -> %s (%s)\n", d.Policy, d.From.Ref, d.From.RevisionID, d.To.Ref, d.To.RevisionID)
	if d.empty() {
		fmt.Fprintln(out, "\nNo differences.")
		return
	}
	fmt.Fprintln(out)
	for _, cb := range d.Cookbooks {
		switch {
		case cb.From == "":
			fmt.Fprintf(out, "  cookbook  %s  + %s\n", cb.Name, cb.To)
		case cb.To == "":
			fmt.Fprintf(out, "  cookbook  %s  - %s\n", cb.Name, cb.From)
		default:
			fmt.Fprintf(out, "  cookbook  %s  %s -> %s\n", cb.Name, cb.From, cb.To)
		}
	}
	for _, r := range d.RunList.Added {
		fmt.Fprintf(out, "  run_list  + %s\n", r)
	}
	for _, r := range d.RunList.Removed {
		fmt.Fprintf(out, "  run_list  - %s\n", r)
	}
	for _, at := range d.Attributes {
		switch {
		case at.From == nil:
			fmt.Fprintf(out, "  attr  %s  + %v\n", at.Path, at.To)
		case at.To == nil:
			fmt.Fprintf(out, "  attr  %s  - %v\n", at.Path, at.From)
		default:
			fmt.Fprintf(out, "  attr  %s  %v -> %v\n", at.Path, at.From, at.To)
		}
	}
}

// newPolicyCleanCmd builds `cinc policy clean [name]`. It deletes policy
// revisions that no policy group references.
func newPolicyCleanCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "clean [name]",
		Short: "Delete policy revisions that no policy group references",
		Example: `Delete revisions of a policy that no policy group references.
cinc policy clean appserver`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			inUse, err := revisionsInUse(cmd.Context(), c)
			if err != nil {
				return err
			}
			index, _, err := c.Policies.List(cmd.Context())
			if err != nil {
				return err
			}

			names := make([]string, 0, len(index))
			for name := range index {
				if len(args) == 1 && name != args[0] {
					continue
				}
				names = append(names, name)
			}
			slices.Sort(names)

			out := cmd.OutOrStdout()
			for _, name := range names {
				var orphans, kept []string
				for rev := range index[name].Revisions {
					if _, used := inUse[name+"@"+rev]; used {
						kept = append(kept, rev)
						continue
					}
					orphans = append(orphans, rev)
				}
				slices.Sort(orphans)
				if len(orphans) == 0 {
					continue
				}
				for _, rev := range orphans {
					if dryRun {
						continue
					}
					if _, err := c.Policies.DeleteRevision(cmd.Context(), name, rev); err != nil {
						return err
					}
				}
				verb := "Deleted"
				if dryRun {
					verb = "Would delete"
				}
				fmt.Fprintf(out, "%s %s (%d orphaned revision(s)): %v\n", verb, name, len(orphans), orphans)
				if len(kept) > 0 {
					fmt.Fprintf(out, "Kept %d revision(s) still in use by a policy group\n", len(kept))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be deleted without deleting anything")
	return cmd
}

// revisionsInUse returns the set of "<policy>@<revision>" keys pinned by any
// policy group.
func revisionsInUse(ctx context.Context, c *cinc.Client) (map[string]struct{}, error) {
	groups, _, err := c.PolicyGroups.List(ctx)
	if err != nil {
		return nil, err
	}
	inUse := map[string]struct{}{}
	for _, g := range groups {
		for policy, assignment := range g.Policies {
			inUse[policy+"@"+assignment.RevisionID] = struct{}{}
		}
	}
	return inUse, nil
}

// cookbookArtifactRef names one cookbook artifact version: its cookbook name and
// the content identifier the artifact is stored under.
type cookbookArtifactRef struct {
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
}

// newPolicyCleanCookbooksCmd builds `cinc policy clean-cookbooks`. It deletes
// cookbook artifacts (/cookbook_artifacts/NAME/IDENTIFIER) that no policy
// revision references — the artifact-level complement to `policy clean`, which
// prunes unreferenced revisions.
func newPolicyCleanCookbooksCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "clean-cookbooks",
		Short: "Delete cookbook artifacts that no policy revision references",
		Long: "Delete cookbook artifacts (/cookbook_artifacts/NAME/IDENTIFIER) that no\n" +
			"policy revision references.\n\n" +
			"Tip: run `cinc policy clean` first. Pruning unreferenced revisions can\n" +
			"orphan more cookbook artifacts, which this command will then clean up.",
		Example: `Delete cookbook artifacts no policy revision uses.
cinc policy clean-cookbooks
Preview what would be deleted without deleting anything.
cinc policy clean-cookbooks --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}

			referenced, err := referencedArtifacts(cmd.Context(), c)
			if err != nil {
				return err
			}
			orphans, err := orphanedArtifacts(cmd.Context(), c, referenced)
			if err != nil {
				return err
			}

			if !dryRun {
				for _, o := range orphans {
					if _, err := c.CookbookArtifacts.Delete(cmd.Context(), o.Name, o.Identifier); err != nil {
						return err
					}
				}
			}
			return emitCleanCookbooks(cmd, format, orphans, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be deleted without deleting anything")
	return cmd
}

// referencedArtifacts returns the set of "<cookbook>@<identifier>" artifacts
// pinned by any revision of any policy. It walks every policy, every revision,
// and every cookbook lock so an artifact is considered orphaned only when no
// revision anywhere still points at it.
func referencedArtifacts(ctx context.Context, c *cinc.Client) (map[string]struct{}, error) {
	index, _, err := c.Policies.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	slices.Sort(names)

	referenced := map[string]struct{}{}
	for _, name := range names {
		revs, _, err := c.Policies.Get(ctx, name)
		if err != nil {
			return nil, err
		}
		for revID := range revs.Revisions {
			rev, _, err := c.Policies.GetRevision(ctx, name, revID)
			if err != nil {
				return nil, err
			}
			for cookbook, lock := range rev.CookbookLocks {
				referenced[cookbook+"@"+lock.Identifier] = struct{}{}
			}
		}
	}
	return referenced, nil
}

// orphanedArtifacts lists every cookbook artifact version not in referenced,
// sorted by name then identifier for stable output.
func orphanedArtifacts(ctx context.Context, c *cinc.Client, referenced map[string]struct{}) ([]cookbookArtifactRef, error) {
	artifacts, _, err := c.CookbookArtifacts.List(ctx)
	if err != nil {
		return nil, err
	}
	var orphans []cookbookArtifactRef
	for name, entry := range artifacts {
		for _, v := range entry.Versions {
			if _, ok := referenced[name+"@"+v.Identifier]; ok {
				continue
			}
			orphans = append(orphans, cookbookArtifactRef{Name: name, Identifier: v.Identifier})
		}
	}
	slices.SortFunc(orphans, func(a, b cookbookArtifactRef) int {
		if n := cmpString(a.Name, b.Name); n != 0 {
			return n
		}
		return cmpString(a.Identifier, b.Identifier)
	})
	return orphans, nil
}

// emitCleanCookbooks renders the clean-cookbooks result in the chosen format.
func emitCleanCookbooks(cmd *cobra.Command, format printer.Format, orphans []cookbookArtifactRef, dryRun bool) error {
	if format == printer.FormatJSON {
		if orphans == nil {
			orphans = []cookbookArtifactRef{}
		}
		return printer.New(cmd.OutOrStdout(), format).Value(map[string]any{
			"dry_run": dryRun,
			"deleted": orphans,
		})
	}
	out := cmd.OutOrStdout()
	if len(orphans) == 0 {
		fmt.Fprintln(out, "No orphaned cookbook artifacts to delete.")
		return nil
	}
	verb := "Deleted"
	if dryRun {
		verb = "Would delete"
	}
	fmt.Fprintf(out, "%s %d orphaned cookbook artifact(s):\n", verb, len(orphans))
	for _, o := range orphans {
		fmt.Fprintf(out, "  %s@%s\n", o.Name, o.Identifier)
	}
	return nil
}
