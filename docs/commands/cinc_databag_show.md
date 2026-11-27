## cinc databag show

Show a data bag's item IDs

```
cinc databag show <bag> [flags]
```

### Examples

Show the item IDs in a data bag.

```bash
cinc databag show passwords
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

* [cinc databag](cinc_databag.md)	 - Manage data bags on the Cinc Server

