## cinc org acl revoke

Remove members from a permission

### Synopsis

Remove users, clients, or groups from one permission's ACE (or all five with `all`).

<perm> is one of create, read, update, delete, grant, or all.
--user, --client, and --group each name one kind of member, so a user and a client
that share a name stay separate. Each flag is repeatable, and at least one is required.

```
cinc org acl revoke <perm> [flags]
```

### Examples

Revoke a group's update access on the current org.

```bash
cinc org acl revoke update --group temps
```

### Options

```
      --client stringArray   client to add or remove (repeatable)
      --group stringArray    group to add or remove (repeatable)
  -h, --help[=true|false]    help for revoke
      --user stringArray     user to add or remove (repeatable)
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org acl](cinc_org_acl.md)	 - Manage the ACL of the current organization

