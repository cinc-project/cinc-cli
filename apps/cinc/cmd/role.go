package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"

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
	cmd.AddCommand(newACLCmd("role", "roles"))
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
			role := cinc.Role{Name: args[0], RunList: []string{}}
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &role); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
				role.Name = args[0]
			}
			if role.RunList == nil {
				role.RunList = []string{}
			}
			if description != "" {
				role.Description = description
			}
			if _, _, err := c.Roles.Create(cmd.Context(), &role); err != nil {
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
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
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
				if reflect.DeepEqual(*current, *edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Role %q unchanged\n", name)
					return nil
				}
				updated = *edited
			}
			updated.Name = name
			if updated.RunList == nil {
				updated.RunList = []string{}
			}

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
			names, err := fetchRoleNames(cmd.Context(), c)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// fetchRoleNames returns the sorted names of every role on the server.
func fetchRoleNames(ctx context.Context, c *cinc.Client) ([]string, error) {
	index, _, err := c.Roles.List(ctx)
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
