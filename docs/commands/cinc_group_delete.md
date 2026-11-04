## cinc group delete

Delete a group from the server

```
cinc group delete <name> [flags]
```

### Examples

Delete a group from the server.

```bash
cinc group delete admins
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

* [cinc group](cinc_group.md)	 - Manage groups on the Cinc Server

