package cmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// aclScope captures what differs between a normal org-scoped object ACL, the
// organization's own ACL, and a global user ACL: which ACL the library
// targets, whether the verbs take a <name> argument, and how to describe the
// target in messages. The read-modify-write lives in the library
// (ACLs.Grant/Revoke); the member flags and perm parsing are shared here.
type aclScope struct {
	// noun is the parent command this acl subgroup hangs under ("node",
	// "org", "user", ...); it drives the help and example text.
	noun string
	// needsName is true when show/grant/revoke take a <name> positional.
	// The org's own ACL has no name; object and user ACLs do.
	needsName bool
	// target names the ACL to read or write. name is "" when needsName is
	// false.
	target func(name string) cinc.ACLTarget
	// describe renders the object for messages, e.g. `node "web01"` or
	// `organization "acme"`.
	describe func(c *cinc.Client, name string) string
}

// newACLCmd builds the `acl` subgroup for a normal org-scoped object whose
// ACL lives at /organizations/<org>/<objectType>/<name>/_acl. noun is the
// parent command name (for help text); objectType is the URL segment, one of
// the cinc.ACL* constants.
func newACLCmd(noun, objectType string) *cobra.Command {
	return newACLGroup(aclScope{
		noun:      noun,
		needsName: true,
		target:    func(name string) cinc.ACLTarget { return cinc.ObjectACL(objectType, name) },
		describe:  func(_ *cinc.Client, name string) string { return fmt.Sprintf("%s %q", noun, name) },
	})
}

// newOrgACLCmd builds the `cinc org acl` subgroup. It manages the ACL of the
// organization object itself, so its verbs take no <name>. The org is
// whichever one the current profile points at.
func newOrgACLCmd() *cobra.Command {
	return newACLGroup(aclScope{
		noun:      "org",
		needsName: false,
		target:    func(string) cinc.ACLTarget { return cinc.OrgACL() },
		describe:  func(c *cinc.Client, _ string) string { return fmt.Sprintf("organization %q", c.Org()) },
	})
}

// newUserACLCmd builds the `cinc user acl` subgroup. User ACLs are global,
// not under an org, so they take a <name> but ignore the profile's org.
func newUserACLCmd() *cobra.Command {
	return newACLGroup(aclScope{
		noun:      "user",
		needsName: true,
		target:    cinc.UserACL,
		describe:  func(_ *cinc.Client, name string) string { return fmt.Sprintf("user %q", name) },
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
			name := aclName(scope, cmdArgs)
			acl, _, err := c.ACLs.GetTarget(cmd.Context(), scope.target(name))
			if err != nil {
				return explainACLError(err, scope.describe(c, name), "")
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
			perm := cmdArgs[0]
			if _, err := cinc.ExpandPerm(perm); err != nil {
				return fmt.Errorf("%q isn't a permission we know. Use one of create, read, update, delete, grant, or all", perm)
			}
			actors := append(append([]string{}, users...), clients...)
			if len(actors) == 0 && len(groups) == 0 {
				return fmt.Errorf("we need at least one member to %s: pass --user, --client, or --group", verb)
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := aclName(scope, cmdArgs)
			target := scope.describe(c, name)
			members := strings.Join(append(actors, groups...), ", ")

			change := c.ACLs.Revoke
			if grant {
				change = c.ACLs.Grant
			}
			changed, err := change(cmd.Context(), scope.target(name), perm, actors, groups)
			if err != nil {
				return explainACLChangeError(err, grant, target, members, groups)
			}

			out := cmd.OutOrStdout()
			if len(changed) == 0 {
				plural := len(actors)+len(groups) > 1
				has := cases(plural, "already have", "already has")
				if !grant {
					has = cases(plural, "don't have", "doesn't have")
				}
				fmt.Fprintf(out, "No change: %s %s %s on %s.\n", members, has, perm, target)
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

// aclError is an ACL request failure retold in terms of the object, keeping
// the server's error underneath for errors.Is.
type aclError struct {
	msg string
	err error
}

func (e *aclError) Error() string { return e.msg }
func (e *aclError) Unwrap() error { return e.err }

// explainACLError turns the server's refusal of an ACL read or write into a
// sentence about target. change describes the attempted write ("grant read
// on node \"web01\" to ops"), or is "" for a read. Errors it has nothing to
// add to pass through unchanged.
func explainACLError(err error, target, change string) error {
	var msg string
	switch {
	case errors.Is(err, cinc.ErrForbidden):
		msg = fmt.Sprintf("you don't have grant permission on %s, and you need it to see or change its ACL. "+
			"Ask an org admin, or anyone who has grant on it, to do this for you", target)
	case errors.Is(err, cinc.ErrNotFound):
		msg = fmt.Sprintf("we couldn't find %s on the server (%s)", target, serverMessage(err))
	case errors.Is(err, cinc.ErrBadRequest) && change != "":
		msg = fmt.Sprintf("we couldn't %s: the server said %q. Check that every user, client, and group you named exists in this org",
			change, serverMessage(err))
	default:
		return err
	}
	return &aclError{err: err, msg: msg}
}

// explainACLChangeError explains a failed grant or revoke. A failure reading
// the ACL is explained as a read. A failed write names the permission that
// failed and, since each permission is its own request, the ones already
// changed before it, so the user knows what state the ACL is left in.
func explainACLChangeError(err error, grant bool, target, members string, groups []string) error {
	var partial *cinc.ACLChangeError
	if !errors.As(err, &partial) {
		return explainACLError(err, target, "")
	}
	verb, preposition := cases(grant, "grant", "revoke"), cases(grant, "to", "from")
	change := fmt.Sprintf("%s %s on %s %s %s", verb, partial.Perm, target, preposition, members)

	var explained error
	if !grant && partial.Perm == "grant" && slices.Contains(groups, "admins") && errors.Is(err, cinc.ErrForbidden) {
		// erchef refuses to take admins off grant for anyone but the
		// superuser, with a 403 that says nothing about the caller's own
		// grant permission.
		explained = &aclError{err: err, msg: fmt.Sprintf(
			"we couldn't %s: the server said %q. Chef servers only let the superuser (%s) take the admins group off the grant permission",
			change, serverMessage(err), cinc.SuperuserName)}
	} else {
		explained = explainACLError(err, target, change)
		if explained == err {
			// Nothing to add about the cause; still say which write failed.
			explained = &aclError{err: err, msg: fmt.Sprintf("we couldn't %s: %s", change, serverMessage(err))}
		}
	}
	if len(partial.Changed) == 0 {
		return explained
	}
	return &aclError{err: err, msg: fmt.Sprintf("%s. We'd already %s %s before that, and those changes are still in place",
		explained.Error(), cases(grant, "granted", "revoked"), strings.Join(partial.Changed, ", "))}
}

// serverMessage returns what the server said about a failed request, or the
// error itself when it isn't a server response.
func serverMessage(err error) string {
	var resp *cinc.ErrorResponse
	if errors.As(err, &resp) {
		if msg := resp.ServerMessage(); msg != "" {
			return msg
		}
	}
	return err.Error()
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
