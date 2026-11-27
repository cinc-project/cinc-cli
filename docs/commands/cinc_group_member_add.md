## cinc group member add

Add actors to a group

```
cinc group member add <group> <name>... [flags]
```

### Examples

Add users or clients to a group.

```bash
cinc group member add admins alice worker-01
```

### Options

```
  -h, --help[=true|false]   help for add
      --type string         actor type to change: user, client, or group (default "user")
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc group member](cinc_group_member.md)	 - Add or remove members of a group

