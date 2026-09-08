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

// newEnvironmentCmd builds the `cinc environment` command group.
func newEnvironmentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "environment",
		Short: "Manage environments on the Cinc Server",
	}
	cmd.AddCommand(newEnvironmentListCmd())
	cmd.AddCommand(newEnvironmentShowCmd())
	cmd.AddCommand(newEnvironmentCreateCmd())
	cmd.AddCommand(newEnvironmentEditCmd())
	cmd.AddCommand(newEnvironmentDeleteCmd())
	cmd.AddCommand(newACLCmd("environment", "environments"))
	return cmd
}

// newEnvironmentEditCmd builds the `cinc environment edit <name>` command. It
// fetches the environment, opens its JSON in the shared editor, and PUTs the
// result back. The path arg pins the name. `--file` reads the updated JSON
// from disk for scripted use.
func newEnvironmentEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit an environment on the server",
		Example: `Edit an environment's cookbook version constraints and attributes.
cinc environment edit prod`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]

			var updated cinc.Environment
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
			} else {
				current, _, err := c.Environments.Get(cmd.Context(), name)
				if err != nil {
					return err
				}
				edited, err := editEnvironment(current)
				if err != nil {
					return err
				}
				if reflect.DeepEqual(*current, *edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Environment %q unchanged\n", name)
					return nil
				}
				updated = *edited
			}
			updated.Name = name

			if _, _, err := c.Environments.Update(cmd.Context(), &updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated environment %q\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated environment JSON from this file instead of launching the editor")
	return cmd
}

// newEnvironmentShowCmd builds the `cinc environment show <name>` command.
func newEnvironmentShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show an environment",
		Example: `Show an environment.
cinc environment show prod`,
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
			env, _, err := c.Environments.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(env)
		},
	}
}

// newEnvironmentCreateCmd builds the `cinc environment create <name>`
// command. By default it POSTs a minimal environment carrying just the
// name and an optional --description. With --file the full environment
// JSON is read from disk, with the positional name overriding whatever
// "name" the file declares so `cinc environment create staging --file
// prod.json` can never silently land in the wrong slot.
func newEnvironmentCreateCmd() *cobra.Command {
	var (
		description string
		inputFile   string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an environment on the server",
		Example: `Create an environment; your editor opens to define it.
cinc environment create prod`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			env := cinc.Environment{Name: args[0]}
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &env); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
				env.Name = args[0]
			}
			if description != "" {
				env.Description = description
			}
			if _, _, err := c.Environments.Create(cmd.Context(), &env); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created environment %q\n", env.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "human-readable description for the new environment")
	cmd.Flags().StringVar(&inputFile, "file", "", "read the full environment JSON from this file instead of using flags")
	return cmd
}

// newEnvironmentDeleteCmd builds the `cinc environment delete <name>` command.
func newEnvironmentDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an environment from the server",
		Example: `Delete an environment from the server.
cinc environment delete prod`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.Environments.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted environment %q\n", name)
			return nil
		},
	}
}

// newEnvironmentListCmd builds the `cinc environment list` command.
func newEnvironmentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List environments on the server",
		Example: `List every environment on the server.
cinc environment list`,
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
			names, err := fetchEnvironmentNames(cmd.Context(), c)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// fetchEnvironmentNames returns the sorted names of every environment on
// the server.
func fetchEnvironmentNames(ctx context.Context, c *cinc.Client) ([]string, error) {
	index, _, err := c.Environments.List(ctx)
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
