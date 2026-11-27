## cinc environment acl revoke

Remove members from a permission

### Synopsis

Remove users, clients, or groups from one permission's ACE (or all five with `all`).

<perm> is one of create, read, update, delete, grant, or all.
--user and --client both target the ACE's actor list (the server treats users
and clients as one actor namespace); --group targets its group list. Each flag is
repeatable, and at least one is required.

```
cinc environment acl revoke <perm> <name> [flags]
```

### Examples

Revoke a user's update access on environment prod.

```bash
cinc environment acl revoke update prod --user alice
```

### Options

```
      --client stringArray   client to add or remove (repeatable; targets the actor list)
      --group stringArray    group to add or remove (repeatable; targets the group list)
  -h, --help[=true|false]    help for revoke
      --user stringArray     user to add or remove (repeatable; targets the actor list)
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc environment acl](cinc_environment_acl.md)	 - Manage the ACL of a environment

