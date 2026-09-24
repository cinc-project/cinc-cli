package cmd

import (
	"encoding/json"
	"fmt"
	"os"
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
				if unchanged(*current, *edited) {
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
			// Check --type before anything talks to the server.
			if _, err := memberSlice(&cinc.Group{}, memberKind(kind)); err != nil {
				return err
			}
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
			changed, unchanged, err := applyMemberChange(current, memberKind(kind), names, add)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(changed) == 0 {
				fmt.Fprintf(out, "No change: %s %s group %q.\n",
					strings.Join(unchanged, ", "), memberState(add, len(unchanged) > 1, false), group)
				return nil
			}
			if _, _, err := c.Groups.Update(cmd.Context(), current); err != nil {
				return err
			}
			// erchef accepts a group PUT naming an actor that does not exist
			// and silently drops it, answering with the body as sent. Read
			// the group back so an add reports only what actually landed.
			var dropped []string
			if add {
				if changed, dropped, err = keptMembers(cmd, c, group, memberKind(kind), changed); err != nil {
					return err
				}
			}
			note := ""
			if len(unchanged) > 0 {
				note = fmt.Sprintf(" (%s %s it)", strings.Join(unchanged, ", "),
					memberState(add, len(unchanged) > 1, true))
			}
			if len(changed) > 0 {
				fmt.Fprintf(out, "%s %s %s group %q%s\n",
					cases(add, "Added", "Removed"), strings.Join(changed, ", "), preposition, group, note)
			}
			if len(dropped) > 0 {
				return fmt.Errorf("the server didn't add %s to group %q. Check that a %s by that name exists in this organization",
					strings.Join(dropped, ", "), group, kind)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "type", string(memberUser), "actor type to change: user, client, or group")
	return cmd
}

// keptMembers reads group back after an add and splits the names the add
// sent into those the group now holds and those the server dropped.
func keptMembers(cmd *cobra.Command, c *cinc.Client, group string, kind memberKind, added []string) (kept, dropped []string, err error) {
	after, _, err := c.Groups.Get(cmd.Context(), group)
	if err != nil {
		return nil, nil, err
	}
	members, err := memberSlice(after, kind)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range added {
		if slices.Contains(*members, name) {
			kept = append(kept, name)
		} else {
			dropped = append(dropped, name)
		}
	}
	return kept, dropped, nil
}

// applyMemberChange adds or removes names from the actor list of the
// given kind on group, in place. It reports which names it changed and
// which were already as asked (already a member for add, not a member for
// remove), each in argument order.
func applyMemberChange(group *cinc.Group, kind memberKind, names []string, add bool) (changed, unchanged []string, err error) {
	target, err := memberSlice(group, kind)
	if err != nil {
		return nil, nil, err
	}
	result := *target
	for _, name := range names {
		if slices.Contains(result, name) == add {
			if !slices.Contains(unchanged, name) {
				unchanged = append(unchanged, name)
			}
			continue
		}
		if add {
			result = append(result, name)
		} else {
			result = slices.DeleteFunc(result, func(n string) bool { return n == name })
		}
		changed = append(changed, name)
	}
	*target = result
	return changed, unchanged, nil
}

// memberState describes names a member change left as they were: already
// in the group for add, not in it for remove. past selects the past tense,
// for a note on a change that did something else.
func memberState(add, plural, past bool) string {
	var forms [4]string // singular present, plural present, singular past, plural past
	if add {
		forms = [4]string{"is already in", "are already in", "was already in", "were already in"}
	} else {
		forms = [4]string{"isn't in", "aren't in", "wasn't in", "weren't in"}
	}
	i := 0
	if plural {
		i++
	}
	if past {
		i += 2
	}
	return forms[i]
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
			names, err := listNames(cmd.Context(), c.Groups.List)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}
