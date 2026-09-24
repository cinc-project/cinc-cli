package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newOrgCmd builds the `cinc org` command group, the cinc equivalent of
// knife's `opc org` commands.
//
// Two scopes live under this one noun, and the help text leans on that
// distinction because it changes which identity and profile you need:
//
//   - list/show/create/edit/delete talk to the server root (/organizations),
//     so they need a pivotal (superuser) identity.
//   - member and invite act on whichever org your profile's server URL points
//     at. knife takes the org as an explicit argument; cinc derives it from
//     the profile, so to manage a different org you switch --profile.
func newOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Manage organizations on the Cinc Server",
		Long: `Manage organizations on the Cinc Server.

The list, show, create, edit, and delete verbs talk to the server root
(/organizations), so they need a pivotal (superuser) identity, the kind of
account that signs the server's own administrative requests. Point a profile at
such an identity to use them.

The member and invite subgroups are organization-scoped: they act on whichever
org the current profile's server URL points at. knife takes the org as an
explicit argument; cinc derives it from your profile instead, so to manage a
different org, switch profiles with --profile.`,
	}
	cmd.AddCommand(newOrgListCmd())
	cmd.AddCommand(newOrgShowCmd())
	cmd.AddCommand(newOrgCreateCmd())
	cmd.AddCommand(newOrgEditCmd())
	cmd.AddCommand(newOrgDeleteCmd())
	cmd.AddCommand(newOrgMemberCmd())
	cmd.AddCommand(newOrgInviteCmd())
	cmd.AddCommand(newOrgACLCmd())
	return cmd
}

// newOrgListCmd builds the `cinc org list` command.
func newOrgListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List organizations on the server",
		Long: `List every organization on the server.

Listing every org needs a pivotal (superuser) identity. If the server won't
let you, you get the organizations your user belongs to instead, with a note
on stderr saying so.`,
		Example: `List every organization on the server.
cinc org list`,
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
			names, err := listNames(cmd.Context(), c.Orgs.List)
			if errors.Is(err, cinc.ErrForbidden) {
				names, err = fetchMemberOrgNames(cmd, c, err)
			}
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// fetchMemberOrgNames is org list's answer when the server refuses to list
// every org, which erchef does for anyone but pivotal: the sorted names of
// the orgs the signing user belongs to, from /users/NAME/organizations, with
// a note on stderr so stdout stays a plain list. When the signer is not a
// user (a client has no such endpoint), it returns forbidden, the server's
// original refusal.
func fetchMemberOrgNames(cmd *cobra.Command, c *cinc.Client, forbidden error) ([]string, error) {
	name := c.ClientName()
	orgs, _, err := c.Associations.ListUserOrgs(cmd.Context(), name)
	if err != nil {
		return nil, forbidden
	}
	names := make([]string, 0, len(orgs))
	for _, o := range orgs {
		names = append(names, o.Name)
	}
	slices.Sort(names)
	fmt.Fprintf(cmd.ErrOrStderr(),
		"You can't list every organization on this server, so here are the ones %q belongs to.\n", name)
	return names, nil
}

// newOrgShowCmd builds the `cinc org show <org>` command.
func newOrgShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <org>",
		Short: "Show an organization",
		Long: `Show an organization's metadata.

This hits the server root, so it needs a pivotal (superuser) identity.`,
		Example: `Show an organization's metadata.
cinc org show acme`,
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
			org, _, err := c.Orgs.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(org)
		},
	}
}

// newOrgCreateCmd builds the `cinc org create <shortname> <fullname>` command.
//
// The server provisions a validator client for the new org and returns its
// private key once, at creation time only. The key is written to --filename
// when given, otherwise streamed to stdout with a heads-up on stderr that it
// can't be retrieved again.
func newOrgCreateCmd() *cobra.Command {
	var keyFile string
	cmd := &cobra.Command{
		Use:   "create <shortname> <fullname>",
		Short: "Create an organization on the server",
		Long: `Create an organization on the server.

The server provisions a validator client and returns its private key exactly
once, right here. Capture it now; it can't be retrieved later. Use --filename
to write it straight to disk.

This hits the server root, so it needs a pivotal (superuser) identity.`,
		Example: `Create an org and write its validator key to a file.
cinc org create acme "Acme Corporation" --filename acme-validator.pem

Create an org and stream the validator key to stdout.
cinc org create acme "Acme Corporation"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			created, _, err := c.Orgs.Create(cmd.Context(), &cinc.Org{
				Name:     args[0],
				FullName: args[1],
			})
			if err != nil {
				return err
			}
			return emitPrivateKey(cmd, fmt.Sprintf("Created organization %q", args[0]), "validator key", created.PrivateKey, keyFile)
		},
	}
	cmd.Flags().StringVarP(&keyFile, "filename", "f", "", "write the generated validator private key to this file instead of stdout")
	return cmd
}

// newOrgEditCmd builds the `cinc org edit <org>` command. It fetches the org,
// opens its JSON (typically to change the full name) in the shared editor, and
// PUTs the result back. `--file` reads the updated JSON from disk for scripted
// use, and an unchanged object is a friendly no-op.
func newOrgEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <org>",
		Short: "Edit an organization on the server",
		Long: `Edit an organization, typically to change its full name.

Fetches the org, opens its JSON in your editor, and saves the result back. Use
--file to supply the JSON non-interactively.

This hits the server root, so it needs a pivotal (superuser) identity.`,
		Example: `Open an org's JSON in your editor and save it back.
cinc org edit acme`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]

			var updated cinc.Org
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
			} else {
				current, _, err := c.Orgs.Get(cmd.Context(), name)
				if err != nil {
					return err
				}
				edited, err := editOrg(current)
				if err != nil {
					return err
				}
				if unchanged(*current, *edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Organization %q unchanged\n", name)
					return nil
				}
				updated = *edited
			}
			updated.Name = name

			if _, _, err := c.Orgs.Update(cmd.Context(), &updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated organization %q\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated org JSON from this file instead of launching the editor")
	return cmd
}

// newOrgDeleteCmd builds the `cinc org delete <org>` command.
func newOrgDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <org>",
		Short: "Delete an organization from the server",
		Long: `Delete an organization from the server.

This hits the server root, so it needs a pivotal (superuser) identity.`,
		Example: `Delete an organization from the server.
cinc org delete acme`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.Orgs.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted organization %q\n", name)
			return nil
		},
	}
}

// newOrgMemberCmd builds the `cinc org member` subgroup, which manages the
// membership of the org the current profile points at.
func newOrgMemberCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "member",
		Short: "List, add, or remove members of the current org",
		Long: `Manage the membership of the organization your profile points at.

These verbs are organization-scoped: they act on whichever org the current
profile's server URL targets, not on an org named on the command line. To
manage a different org, switch profiles with --profile.`,
	}
	cmd.AddCommand(newOrgMemberListCmd())
	cmd.AddCommand(newOrgMemberAddCmd())
	cmd.AddCommand(newOrgMemberRemoveCmd())
	return cmd
}

// newOrgMemberListCmd builds `cinc org member list`.
func newOrgMemberListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List members of the current org",
		Example: `List the members of the org your profile points at.
cinc org member list`,
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
			names, _, err := c.Associations.ListMembers(cmd.Context())
			if err != nil {
				return err
			}
			slices.Sort(names)
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// newOrgMemberAddCmd builds `cinc org member add <user>`. This immediately
// associates an existing user with the org, which is a superuser operation;
// most non-pivotal callers should use `org invite create` instead.
func newOrgMemberAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <user>",
		Short: "Add a user to the current org",
		Long: `Immediately associate an existing user with the current org.

This is a superuser operation; if you're not pivotal, use ` + "`cinc org invite create`" + ` to
send an invitation the user accepts instead.`,
		Example: `Add an existing user to the org your profile points at.
cinc org member add alice`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			user := args[0]
			if _, err := c.Associations.AddMember(cmd.Context(), user); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added %q to organization %q\n", user, c.Org())
			return nil
		},
	}
}

// newOrgMemberRemoveCmd builds `cinc org member remove <user>`.
func newOrgMemberRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <user>",
		Short: "Remove a user from the current org",
		Example: `Remove a user from the org your profile points at.
cinc org member remove alice`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			user := args[0]
			if _, _, err := c.Associations.RemoveMember(cmd.Context(), user); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %q from organization %q\n", user, c.Org())
			return nil
		},
	}
}

// newOrgInviteCmd builds the `cinc org invite` subgroup, which manages pending
// invitations (association requests) for the org the current profile points at.
func newOrgInviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "List, create, or rescind invitations for the current org",
		Long: `Manage pending invitations for the organization your profile points at.

Like the member subgroup, these verbs are organization-scoped: they act on
whichever org the current profile's server URL targets. To manage a different
org, switch profiles with --profile.`,
	}
	cmd.AddCommand(newOrgInviteListCmd())
	cmd.AddCommand(newOrgInviteCreateCmd())
	cmd.AddCommand(newOrgInviteRescindCmd())
	return cmd
}

// newOrgInviteListCmd builds `cinc org invite list`. It renders each pending
// invitation in full (id and username) so the id needed by `invite rescind` is
// visible — `printer.Value` gives a JSON array under --format json and pretty
// JSON for humans.
func newOrgInviteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List pending invitations for the current org",
		Example: `List the pending invitations for the org your profile points at.
cinc org invite list`,
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
			invites, _, err := c.Associations.ListInvites(cmd.Context())
			if err != nil {
				return err
			}
			if invites == nil {
				invites = []cinc.Invitation{}
			}
			return printer.New(cmd.OutOrStdout(), format).Value(invites)
		},
	}
}

// newOrgInviteCreateCmd builds `cinc org invite create <user>`.
func newOrgInviteCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <user>",
		Short: "Invite a user to the current org",
		Example: `Invite a user to the org your profile points at.
cinc org invite create carol`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			user := args[0]
			if _, _, err := c.Associations.Invite(cmd.Context(), user); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Invited %q to organization %q\n", user, c.Org())
			return nil
		},
	}
}

// newOrgInviteRescindCmd builds `cinc org invite rescind <id>`. The id comes
// from `org invite list`.
func newOrgInviteRescindCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rescind <id>",
		Short: "Cancel a pending invitation for the current org",
		Long: `Cancel a pending invitation by its id.

Run ` + "`cinc org invite list`" + ` to find the id of the invitation you want to cancel.`,
		Example: `Cancel a pending invitation by its id.
cinc org invite rescind acme-carol`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if _, err := c.Associations.RescindInvite(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Rescinded invitation %q\n", id)
			return nil
		},
	}
}
