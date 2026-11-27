## cinc databag item list

List items in a data bag

```
cinc databag item list <bag> [flags]
```

### Examples

List the item IDs in a data bag.

```bash
cinc databag item list passwords
```

### Options

```
  -h, --help[=true|false]   help for list
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc databag item](cinc_databag_item.md)	 - Manage items within a data bag

