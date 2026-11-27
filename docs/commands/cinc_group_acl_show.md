## cinc group acl show

Show the full ACL

### Synopsis

Show all five permissions of the ACL and the actors and groups each grants.

```
cinc group acl show <name> [flags]
```

### Examples

Show the full ACL of group admins.

```bash
cinc group acl show admins
```

### Options

```
  -h, --help[=true|false]   help for show
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc group acl](cinc_group_acl.md)	 - Manage the ACL of a group

