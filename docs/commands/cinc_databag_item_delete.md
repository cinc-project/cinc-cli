## cinc databag item delete

Delete a data bag item from the server

```
cinc databag item delete <bag> <id> [flags]
```

### Examples

Delete an item from a data bag.

```bash
cinc databag item delete passwords mysql
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

* [cinc databag item](cinc_databag_item.md)	 - Manage items within a data bag

