package cmd

import (
	"fmt"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newRoleCmd builds the `cinc role` command group.
func newRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Manage roles on the Cinc Server",
	}
	cmd.AddCommand(newRoleListCmd())
	cmd.AddCommand(newRoleShowCmd())
	cmd.AddCommand(newRoleCreateCmd())
	cmd.AddCommand(newRoleEditCmd())
	cmd.AddCommand(newRoleDeleteCmd())
	cmd.AddCommand(newACLCmd("role", cinc.ACLRoles))
	return cmd
}

// newRoleCreateCmd builds the `cinc role create <name>` command. By default
// it POSTs a minimal role carrying just the name, an empty run list, and an
// optional --description. With --file the full role JSON is read from disk,
// with the positional name overriding whatever "name" the file declares.
func newRoleCreateCmd() *cobra.Command {
	var (
		description string
		inputFile   string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a role on the server",
		Example: `Create a role; your editor opens to define its run-list and attributes.
cinc role create webserver`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			role := cinc.Role{Name: args[0]}
			if inputFile != "" {
				if role, err = readJSONFile[cinc.Role](inputFile); err != nil {
					return err
				}
				role.Name = args[0]
			}
			if description != "" {
				role.Description = description
			}
			if _, err := c.Roles.Create(cmd.Context(), &role); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created role %q\n", role.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "human-readable description for the new role")
	cmd.Flags().StringVar(&inputFile, "file", "", "read the full role JSON from this file instead of using flags")
	return cmd
}

// newRoleEditCmd builds the `cinc role edit <name>` command. It fetches the
// role, opens its JSON in the shared editor, and PUTs the result back. The
// path arg pins the role name so an edit can't rename it. `--file` reads the
// updated JSON from disk for scripted use.
func newRoleEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit a role on the server",
		Example: `Edit a role's run-list and attributes in your editor.
cinc role edit webserver`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]

			var updated cinc.Role
			if inputFile != "" {
				if updated, err = readJSONFile[cinc.Role](inputFile); err != nil {
					return err
				}
			} else {
				current, _, err := c.Roles.Get(cmd.Context(), name)
				if err != nil {
					return err
				}
				edited, err := editRole(current)
				if err != nil {
					return err
				}
				if unchanged(*current, *edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Role %q unchanged\n", name)
					return nil
				}
				updated = *edited
			}
			updated.Name = name

			if _, _, err := c.Roles.Update(cmd.Context(), &updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated role %q\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated role JSON from this file instead of launching the editor")
	return cmd
}

// newRoleShowCmd builds the `cinc role show <name>` command.
func newRoleShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a role",
		Example: `Show a role.
cinc role show webserver`,
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
			role, _, err := c.Roles.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(role)
		},
	}
}

// newRoleDeleteCmd builds the `cinc role delete <name>` command.
func newRoleDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a role from the server",
		Example: `Delete a role from the server.
cinc role delete webserver`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.Roles.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted role %q\n", name)
			return nil
		},
	}
}

// newRoleListCmd builds the `cinc role list` command.
func newRoleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List roles on the server",
		Example: `List every role on the server.
cinc role list`,
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
			names, err := listNames(cmd.Context(), c.Roles.List)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}
