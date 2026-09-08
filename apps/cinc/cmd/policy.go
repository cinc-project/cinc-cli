package cmd

import (
	"context"
	"fmt"
	"slices"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newPolicyCmd builds the `cinc policy` command group.
func newPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Manage Policyfile policies on the Cinc Server",
	}
	cmd.AddCommand(newPolicyListCmd())
	cmd.AddCommand(newPolicyShowCmd())
	cmd.AddCommand(newPolicyDeleteCmd())
	cmd.AddCommand(newPolicyCreateCmd())
	cmd.AddCommand(newPolicyInstallCmd())
	cmd.AddCommand(newPolicyDiffCmd())
	cmd.AddCommand(newPolicyCleanCmd())
	cmd.AddCommand(newPolicyCleanCookbooksCmd())
	cmd.AddCommand(newPolicyPushCmd())
	cmd.AddCommand(newPolicyPushArchiveCmd())
	cmd.AddCommand(newPolicyExportCmd())
	cmd.AddCommand(newACLCmd("policy", "policies"))
	return cmd
}

// newPolicyDeleteCmd builds the `cinc policy delete <name>` command. It
// removes the named policy and every one of its revisions from the
// server.
func newPolicyDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a policy and all its revisions from the server",
		Example: `Delete a policy and all of its revisions.
cinc policy delete appserver`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.Policies.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted policy %q\n", name)
			return nil
		},
	}
}

// newPolicyShowCmd builds the `cinc policy show <name>` command. It
// shows every revision of the named policy, keyed by revision ID.
func newPolicyShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a policy's revisions",
		Example: `Show a policy's revisions.
cinc policy show appserver`,
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
			policy, _, err := c.Policies.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(policy)
		},
	}
}

// newPolicyListCmd builds the `cinc policy list` command.
func newPolicyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List policies on the server",
		Example: `List every policy on the server.
cinc policy list`,
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
			names, err := fetchPolicyNames(cmd.Context(), c)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// fetchPolicyNames returns the sorted names of every policy on the
// server. The list response carries per-policy revision metadata; the
// list verb only enumerates names.
func fetchPolicyNames(ctx context.Context, c *cinc.Client) ([]string, error) {
	index, _, err := c.Policies.List(ctx)
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
