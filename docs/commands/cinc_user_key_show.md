## cinc user key show

Show one of a user's keys

```
cinc user key show <user> <key-name> [flags]
```

### Examples

Show one of a user's keys.

```bash
cinc user key show alice default
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

* [cinc user key](cinc_user_key.md)	 - Manage a user's public keys

