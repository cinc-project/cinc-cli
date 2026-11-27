## cinc databag item create

Create an item in a data bag

```
cinc databag item create <bag> <id> [flags]
```

### Examples

Create an item in a data bag; your editor opens to edit its JSON.

```bash
cinc databag item create passwords mysql
```

### Options

```
      --file string         read the new item JSON from this file instead of launching the editor
  -h, --help[=true|false]   help for create
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc databag item](cinc_databag_item.md)	 - Manage items within a data bag

