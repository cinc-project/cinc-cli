package cmd

import (
	"context"
	"fmt"
	"slices"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newPolicyGroupCmd builds the `cinc policy-group` command group.
func newPolicyGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy-group",
		Short: "Manage policy groups on the Cinc Server",
	}
	cmd.AddCommand(newPolicyGroupListCmd())
	cmd.AddCommand(newPolicyGroupShowCmd())
	cmd.AddCommand(newPolicyGroupDeleteCmd())
	cmd.AddCommand(newACLCmd("policy-group", "policy_groups"))
	return cmd
}

// newPolicyGroupDeleteCmd builds the `cinc policy-group delete <name>`
// command. It removes the named policy group from the server; the
// policies it referenced are left in place.
func newPolicyGroupDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a policy group from the server",
		Example: `Delete a policy group.
cinc policy-group delete prod`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.PolicyGroups.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted policy group %q\n", name)
			return nil
		},
	}
}

// newPolicyGroupShowCmd builds the `cinc policy-group show <name>`
// command. It shows which revision of each policy is active in the
// group.
func newPolicyGroupShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a policy group's active policy revisions",
		Example: `Show the policies and revisions active in a group.
cinc policy-group show prod`,
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
			group, _, err := c.PolicyGroups.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(group)
		},
	}
}

// newPolicyGroupListCmd builds the `cinc policy-group list` command.
func newPolicyGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List policy groups on the server",
		Example: `List every policy group.
cinc policy-group list`,
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
			names, err := fetchPolicyGroupNames(cmd.Context(), c)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// fetchPolicyGroupNames returns the sorted names of every policy group
// on the server.
func fetchPolicyGroupNames(ctx context.Context, c *cinc.Client) ([]string, error) {
	index, _, err := c.PolicyGroups.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}
