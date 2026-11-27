## cinc org member remove

Remove a user from the current org

```
cinc org member remove <user> [flags]
```

### Examples

Remove a user from the org your profile points at.

```bash
cinc org member remove alice
```

### Options

```
  -h, --help[=true|false]   help for remove
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org member](cinc_org_member.md)	 - List, add, or remove members of the current org

