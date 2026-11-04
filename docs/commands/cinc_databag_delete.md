## cinc databag delete

Delete a data bag from the server

```
cinc databag delete <name> [flags]
```

### Examples

Delete a data bag and all of its items.

```bash
cinc databag delete passwords
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

* [cinc databag](cinc_databag.md)	 - Manage data bags on the Cinc Server

