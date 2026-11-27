## cinc role acl grant

Add members to a permission

### Synopsis

Add users, clients, or groups to one permission's ACE (or all five with `all`).

<perm> is one of create, read, update, delete, grant, or all.
--user and --client both target the ACE's actor list (the server treats users
and clients as one actor namespace); --group targets its group list. Each flag is
repeatable, and at least one is required.

```
cinc role acl grant <perm> <name> [flags]
```

### Examples

Grant a group read access to role web.

```bash
cinc role acl grant read web --group admins
```

### Options

```
      --client stringArray   client to add or remove (repeatable; targets the actor list)
      --group stringArray    group to add or remove (repeatable; targets the group list)
  -h, --help[=true|false]    help for grant
      --user stringArray     user to add or remove (repeatable; targets the actor list)
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc role acl](cinc_role_acl.md)	 - Manage the ACL of a role

