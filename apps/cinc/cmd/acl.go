package cmd

import (
	"context"
	"fmt"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// aclScope captures what differs between a normal org-scoped object ACL, the
// organization's own ACL, and a global user ACL: how to read and write the
// ACL, whether the verbs take a <name> argument, and how to describe the
// target in messages. Everything else — the read-modify-write core, the
// member flags, the perm parsing — is shared.
type aclScope struct {
	// noun is the parent command this acl subgroup hangs under ("node",
	// "org", "user", …); it drives the help and example text.
	noun string
	// needsName is true when show/grant/revoke take a <name> positional.
	// The org's own ACL has no name; object and user ACLs do.
	needsName bool
	// get fetches the full ACL. name is "" when needsName is false.
	get func(ctx context.Context, c *cinc.Client, name string) (*cinc.ACL, error)
	// set rewrites one permission's ACE. name is "" when needsName is false.
	set func(ctx context.Context, c *cinc.Client, name, perm string, ace *cinc.ACE) error
	// target renders the object for confirmation messages, e.g.
	// `node "web01"` or `organization "acme"`.
	target func(cmd *cobra.Command, name string) string
}

// newACLCmd builds the `acl` subgroup for a normal org-scoped object whose
// ACL lives at /organizations/<org>/<objectType>/<name>/_acl. noun is the
// parent command name (for help text); objectType is the URL segment.
func newACLCmd(noun, objectType string) *cobra.Command {
	return newACLGroup(aclScope{
		noun:      noun,
		needsName: true,
		get: func(ctx context.Context, c *cinc.Client, name string) (*cinc.ACL, error) {
			acl, _, err := c.ACLs.Get(ctx, objectType, name)
			return acl, err
		},
		set: func(ctx context.Context, c *cinc.Client, name, perm string, ace *cinc.ACE) error {
			return c.ACLs.SetPermission(ctx, objectType, name, perm, ace)
		},
		target: func(_ *cobra.Command, name string) string {
			return fmt.Sprintf("%s %q", noun, name)
		},
	})
}

// newOrgACLCmd builds the `cinc org acl` subgroup. It manages the ACL of the
// organization object itself — served at /organizations/<org>/_acl with no
// object-type segment — so its verbs take no <name>. The org is whichever one
// the current profile points at.
func newOrgACLCmd() *cobra.Command {
	return newACLGroup(aclScope{
		noun:      "org",
		needsName: false,
		get: func(ctx context.Context, c *cinc.Client, _ string) (*cinc.ACL, error) {
			acl, _, err := c.ACLs.GetOrg(ctx)
			return acl, err
		},
		set: func(ctx context.Context, c *cinc.Client, _, perm string, ace *cinc.ACE) error {
			return c.ACLs.SetOrgPermission(ctx, perm, ace)
		},
		target: func(cmd *cobra.Command, _ string) string {
			return fmt.Sprintf("organization %q", orgName(cmd))
		},
	})
}

// newUserACLCmd builds the `cinc user acl` subgroup. User ACLs are global —
// served at /users/<name>/_acl, not under an org — so they take a <name> but
// ignore the profile's org.
func newUserACLCmd() *cobra.Command {
	return newACLGroup(aclScope{
		noun:      "user",
		needsName: true,
		get: func(ctx context.Context, c *cinc.Client, name string) (*cinc.ACL, error) {
			acl, _, err := c.ACLs.GetUser(ctx, name)
			return acl, err
		},
		set: func(ctx context.Context, c *cinc.Client, name, perm string, ace *cinc.ACE) error {
			return c.ACLs.SetUserPermission(ctx, name, perm, ace)
		},
		target: func(_ *cobra.Command, name string) string {
			return fmt.Sprintf("user %q", name)
		},
	})
}

// newACLGroup assembles the show/grant/revoke commands for one scope.
func newACLGroup(scope aclScope) *cobra.Command {
	long := `Manage the access-control list (ACL) of this object.

A Chef ACL grants five permissions (create, read, update, delete, and grant)
to actors (users and clients) and to groups. Editing an ACL requires an
identity that already holds the grant permission on the object.`
	switch scope.noun {
	case "org":
		long += "\n\nThe org ACL is the organization object's own ACL. It applies to whichever " +
			"org the current profile's server URL points at; switch --profile to manage another."
	case "user":
		long += "\n\nUser ACLs are global, not org-scoped: they live at the server root, so they " +
			"need an identity with grant permission on the user object itself."
	}
	cmd := &cobra.Command{
		Use:   "acl",
		Short: "Manage the ACL of " + aclNounPhrase(scope.noun),
		Long:  long,
	}
	cmd.AddCommand(newACLShowCmd(scope))
	cmd.AddCommand(newACLChangeCmd(scope, true))
	cmd.AddCommand(newACLChangeCmd(scope, false))
	return cmd
}

// aclNounPhrase renders the short-help noun phrase for an acl subgroup.
func aclNounPhrase(noun string) string {
	switch noun {
	case "org":
		return "the current organization"
	case "user":
		return "a user"
	default:
		return "a " + noun
	}
}

// newACLShowCmd builds `<noun> acl show [<name>]`.
func newACLShowCmd(scope aclScope) *cobra.Command {
	use, args := "show", cobra.NoArgs
	if scope.needsName {
		use, args = "show <name>", cobra.ExactArgs(1)
	}
	return &cobra.Command{
		Use:     use,
		Short:   "Show the full ACL",
		Long:    "Show all five permissions of the ACL and the actors and groups each grants.",
		Example: aclExample(scope, "show"),
		Args:    args,
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			acl, err := scope.get(cmd.Context(), c, aclName(scope, cmdArgs))
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(acl)
		},
	}
}

// newACLChangeCmd builds either `<noun> acl grant` or `<noun> acl revoke`.
// Both read the current ACL, add or remove the named members in each target
// permission's ACE, and write back only the permissions that actually changed.
func newACLChangeCmd(scope aclScope, grant bool) *cobra.Command {
	verb, alias, preposition := "revoke", "remove", "from"
	if grant {
		verb, alias, preposition = "grant", "add", "to"
	}
	use := verb + " <perm>"
	args := cobra.ExactArgs(1)
	if scope.needsName {
		use = verb + " <perm> <name>"
		args = cobra.ExactArgs(2)
	}

	var users, clients, groups []string
	cmd := &cobra.Command{
		Use:     use,
		Aliases: []string{alias},
		Short:   cases(grant, "Add members to a permission", "Remove members from a permission"),
		Long: cases(grant,
			"Add users, clients, or groups to one permission's ACE (or all five with `all`).",
			"Remove users, clients, or groups from one permission's ACE (or all five with `all`).") +
			"\n\n<perm> is one of create, read, update, delete, grant, or all.\n" +
			"--user and --client both target the ACE's actor list (the server treats users\n" +
			"and clients as one actor namespace); --group targets its group list. Each flag is\n" +
			"repeatable, and at least one is required.",
		Example: aclExample(scope, verb),
		Args:    args,
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			perms, err := cinc.ExpandPerm(cmdArgs[0])
			if err != nil {
				return err
			}
			actors := append(append([]string{}, users...), clients...)
			if len(actors) == 0 && len(groups) == 0 {
				return fmt.Errorf("we need at least one member to %s — pass --user, --client, or --group", verb)
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := aclName(scope, cmdArgs)
			acl, err := scope.get(cmd.Context(), c, name)
			if err != nil {
				return err
			}

			var changed []string
			for _, perm := range perms {
				ace, err := acl.ACEFor(perm)
				if err != nil {
					return err
				}
				var aceChanged bool
				if grant {
					aceChanged = ace.AddMembers(actors, groups)
				} else {
					aceChanged = ace.RemoveMembers(actors, groups)
				}
				if !aceChanged {
					continue
				}
				if err := scope.set(cmd.Context(), c, name, perm, ace); err != nil {
					return err
				}
				changed = append(changed, perm)
			}

			out := cmd.OutOrStdout()
			members := strings.Join(append(actors, groups...), ", ")
			target := scope.target(cmd, name)
			if len(changed) == 0 {
				fmt.Fprintf(out, "No changes — %s on %s already matches that for %s.\n",
					cmdArgs[0], target, members)
				return nil
			}
			fmt.Fprintf(out, "%s %s on %s %s %s\n",
				cases(grant, "Granted", "Revoked"), strings.Join(changed, ", "), target, preposition, members)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&users, "user", nil, "user to add or remove (repeatable; targets the actor list)")
	cmd.Flags().StringArrayVar(&clients, "client", nil, "client to add or remove (repeatable; targets the actor list)")
	cmd.Flags().StringArrayVar(&groups, "group", nil, "group to add or remove (repeatable; targets the group list)")
	return cmd
}

// aclName returns the object name from the positional args, or "" for a
// nameless scope (the org's own ACL). For grant/revoke the perm is args[0]
// and the name is args[1]; for show the name is args[0].
func aclName(scope aclScope, args []string) string {
	if !scope.needsName {
		return ""
	}
	return args[len(args)-1]
}

// aclExample renders a copy-pasteable example line for one verb, adapting to
// whether the scope takes a <name>.
func aclExample(scope aclScope, verb string) string {
	name := aclExampleName(scope.noun)
	switch verb {
	case "show":
		if scope.needsName {
			return fmt.Sprintf("Show the full ACL of %s %s.\ncinc %s acl show %s", scope.noun, name, scope.noun, name)
		}
		return "Show the current organization's own ACL.\ncinc org acl show"
	case "grant":
		if scope.needsName {
			return fmt.Sprintf("Grant a group read access to %s %s.\ncinc %s acl grant read %s --group admins", scope.noun, name, scope.noun, name)
		}
		return "Grant a group read access to the current org.\ncinc org acl grant read --group admins"
	default: // revoke
		if scope.needsName {
			return fmt.Sprintf("Revoke a user's update access on %s %s.\ncinc %s acl revoke update %s --user alice", scope.noun, name, scope.noun, name)
		}
		return "Revoke a group's update access on the current org.\ncinc org acl revoke update --group temps"
	}
}

// aclExampleName picks a representative object name for a noun's examples.
func aclExampleName(noun string) string {
	switch noun {
	case "node":
		return "web01"
	case "user":
		return "alice"
	case "client":
		return "worker-01"
	case "role":
		return "web"
	case "environment":
		return "prod"
	case "databag":
		return "secrets"
	case "group":
		return "admins"
	case "cookbook":
		return "nginx"
	case "policy":
		return "base"
	case "policy-group":
		return "production"
	default:
		return "NAME"
	}
}
