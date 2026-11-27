## cinc group member remove

Remove actors from a group

```
cinc group member remove <group> <name>... [flags]
```

### Examples

Remove an actor from a group.

```bash
cinc group member remove admins alice
```

### Options

```
  -h, --help[=true|false]   help for remove
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

