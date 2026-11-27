## cinc environment acl grant

Add members to a permission

### Synopsis

Add users, clients, or groups to one permission's ACE (or all five with `all`).

<perm> is one of create, read, update, delete, grant, or all.
--user, --client, and --group each name one kind of member, so a user and a client
that share a name stay separate. Each flag is repeatable, and at least one is required.

```
cinc environment acl grant <perm> <name> [flags]
```

### Examples

Grant a group read access to environment prod.

```bash
cinc environment acl grant read prod --group admins
```

### Options

```
      --client stringArray   client to add or remove (repeatable)
      --group stringArray    group to add or remove (repeatable)
  -h, --help[=true|false]    help for grant
      --user stringArray     user to add or remove (repeatable)
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc environment acl](cinc_environment_acl.md)	 - Manage the ACL of a environment

