## cinc policy-group delete

Delete a policy group from the server

```
cinc policy-group delete <name> [flags]
```

### Examples

Delete a policy group.

```bash
cinc policy-group delete prod
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

* [cinc policy-group](cinc_policy-group.md)	 - Manage policy groups on the Cinc Server

