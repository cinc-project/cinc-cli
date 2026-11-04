## cinc user key delete

Delete one of a user's keys

```
cinc user key delete <user> <key-name> [flags]
```

### Examples

Delete one of a user's keys.

```bash
cinc user key delete alice rotation
```

### Options

```
  -h, --help   help for delete
  -y, --yes    accepted for knife compatibility; this command doesn't ask before deleting
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc user key](cinc_user_key.md)	 - Manage a user's public keys

