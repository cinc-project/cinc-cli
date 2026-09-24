package cmd

import (
	"fmt"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"
)

// newNodeEnvironmentSetCmd builds `cinc node environment-set <node> <env>`,
// which sets a node's chef_environment in place, mirroring knife's
// `node environment set`.
func newNodeEnvironmentSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "environment-set <node> <environment>",
		Short: "Set a node's environment",
		Example: `Move a node into a different environment.
cinc node environment-set web01 prod`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name, env := args[0], args[1]
			node, changed, err := c.Nodes.Modify(cmd.Context(), name, func(n *cinc.Node) error {
				n.Environment = env
				return nil
			})
			if err != nil {
				return err
			}
			if !changed {
				fmt.Fprintf(cmd.OutOrStdout(), "Node %q is already in environment %q, so there's nothing to change\n", name, node.EnvironmentName())
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set node %q environment to %q\n", name, node.EnvironmentName())
			return nil
		},
	}
}

// newNodePolicySetCmd builds `cinc node policy-set <node> <policy-group>
// <policy-name>`, which points a node at a Policyfile policy by setting its
// policy_group and policy_name, mirroring knife's `node policy set`.
func newNodePolicySetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "policy-set <node> <policy-group> <policy-name>",
		Short: "Set a node's policy group and policy name",
		Example: `Switch a node to Policyfile-based management.
cinc node policy-set web01 prod base`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name, group, policy := args[0], args[1], args[2]
			node, changed, err := c.Nodes.Modify(cmd.Context(), name, func(n *cinc.Node) error {
				n.PolicyGroup, n.PolicyName = group, policy
				return nil
			})
			if err != nil {
				return err
			}
			if !changed {
				fmt.Fprintf(cmd.OutOrStdout(), "Node %q already uses policy %q in group %q, so there's nothing to change\n", name, node.PolicyName, node.PolicyGroup)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set node %q to policy %q in group %q\n", name, node.PolicyName, node.PolicyGroup)
			return nil
		},
	}
}
