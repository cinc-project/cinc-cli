## cinc databag item show

Show a data bag item on the server

```
cinc databag item show <bag> <id> [flags]
```

### Examples

Show a data bag item.

```bash
cinc databag item show passwords mysql
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

* [cinc databag item](cinc_databag_item.md)	 - Manage items within a data bag

