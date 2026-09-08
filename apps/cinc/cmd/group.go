package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newGroupCmd builds the `cinc group` command group. Groups are the
// ACL actor groups scoped to the configured organization.
func newGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Manage groups on the Cinc Server",
	}
	cmd.AddCommand(newGroupListCmd())
	cmd.AddCommand(newGroupShowCmd())
	cmd.AddCommand(newGroupCreateCmd())
	cmd.AddCommand(newGroupEditCmd())
	cmd.AddCommand(newGroupDeleteCmd())
	cmd.AddCommand(newGroupMemberCmd())
	cmd.AddCommand(newACLCmd("group", "groups"))
	return cmd
}

// newGroupEditCmd builds the `cinc group edit <name>` command. It fetches the
// group, opens its JSON (members and all) in the shared editor, and PUTs the
// result back. The path arg pins the group name. `--file` reads the updated
// JSON from disk for scripted use. For targeted member changes, prefer
// `group member add|remove`.
func newGroupEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit a group's members on the server",
		Example: `Edit a group's membership in your editor.
cinc group edit admins`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]

			var updated cinc.Group
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
			} else {
				current, _, err := c.Groups.Get(cmd.Context(), name)
				if err != nil {
					return err
				}
				edited, err := editGroup(current)
				if err != nil {
					return err
				}
				if reflect.DeepEqual(*current, *edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Group %q unchanged\n", name)
					return nil
				}
				updated = *edited
			}
			updated.Name = name

			if _, _, err := c.Groups.Update(cmd.Context(), &updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated group %q\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated group JSON from this file instead of launching the editor")
	return cmd
}

// newGroupCreateCmd builds the `cinc group create <name>` command. It
// creates an empty group; members are added with `group member add`.
func newGroupCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a group on the server",
		Example: `Create a group.
cinc group create admins`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.Groups.Create(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created group %q\n", name)
			return nil
		},
	}
}

// newGroupDeleteCmd builds the `cinc group delete <name>` command.
func newGroupDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a group from the server",
		Example: `Delete a group from the server.
cinc group delete admins`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.Groups.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted group %q\n", name)
			return nil
		},
	}
}

// newGroupMemberCmd builds the `cinc group member` sub-group, which
// adds and removes actors from a group.
func newGroupMemberCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "member",
		Short: "Add or remove members of a group",
	}
	cmd.AddCommand(newGroupMemberChangeCmd(true))
	cmd.AddCommand(newGroupMemberChangeCmd(false))
	return cmd
}

// memberKind names the three actor lists a group can hold, selected
// with the --type flag.
type memberKind string

const (
	memberUser   memberKind = "user"
	memberClient memberKind = "client"
	memberGroup  memberKind = "group"
)

// newGroupMemberChangeCmd builds either `group member add` or
// `group member remove` depending on add. Both fetch the group, mutate
// the actor list selected by --type, and PUT the result back.
func newGroupMemberChangeCmd(add bool) *cobra.Command {
	verb, preposition := "remove", "from"
	if add {
		verb, preposition = "add", "to"
	}
	var kind string
	cmd := &cobra.Command{
		Use:   verb + " <group> <name>...",
		Short: cases(add, "Add actors to a group", "Remove actors from a group"),
		Example: cases(add,
			`Add users or clients to a group.
cinc group member add admins alice worker-01`,
			`Remove an actor from a group.
cinc group member remove admins alice`),
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			group, names := args[0], args[1:]
			current, _, err := c.Groups.Get(cmd.Context(), group)
			if err != nil {
				return err
			}
			current.Name = group
			if err := applyMemberChange(current, memberKind(kind), names, add); err != nil {
				return err
			}
			if _, _, err := c.Groups.Update(cmd.Context(), current); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s group %q\n",
				cases(add, "Added", "Removed"), strings.Join(names, ", "), preposition, group)
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "type", string(memberUser), "actor type to change: user, client, or group")
	return cmd
}

// applyMemberChange adds or removes names from the actor list of the
// given kind on group, in place.
func applyMemberChange(group *cinc.Group, kind memberKind, names []string, add bool) error {
	target, err := memberSlice(group, kind)
	if err != nil {
		return err
	}
	result := *target
	for _, name := range names {
		if add {
			if !slices.Contains(result, name) {
				result = append(result, name)
			}
			continue
		}
		result = slices.DeleteFunc(result, func(n string) bool { return n == name })
	}
	*target = result
	return nil
}

// memberSlice returns a pointer to the group actor slice selected by
// kind so callers can mutate it directly.
func memberSlice(group *cinc.Group, kind memberKind) (*[]string, error) {
	switch kind {
	case memberUser:
		return &group.Users, nil
	case memberClient:
		return &group.Clients, nil
	case memberGroup:
		return &group.Groups, nil
	default:
		return nil, fmt.Errorf("invalid --type %q: want user, client, or group", kind)
	}
}

// cases returns a when add is true, otherwise b.
func cases(add bool, a, b string) string {
	if add {
		return a
	}
	return b
}

// newGroupShowCmd builds the `cinc group show <name>` command.
func newGroupShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a group's members",
		Example: `Show a group's members.
cinc group show admins`,
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
			group, _, err := c.Groups.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(group)
		},
	}
}

// newGroupListCmd builds the `cinc group list` command.
func newGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List groups on the server",
		Example: `List every group on the server.
cinc group list`,
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
			names, err := fetchGroupNames(cmd.Context(), c)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// fetchGroupNames returns the sorted names of every group in the
// configured organization.
func fetchGroupNames(ctx context.Context, c *cinc.Client) ([]string, error) {
	index, _, err := c.Groups.List(ctx)
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
