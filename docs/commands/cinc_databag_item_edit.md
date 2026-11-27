## cinc databag item edit

Edit a data bag item on the server

```
cinc databag item edit <bag> <id> [flags]
```

### Examples

Edit a data bag item in your editor.

```bash
cinc databag item edit passwords mysql
```

### Options

```
      --file string         read the updated item JSON from this file instead of launching the editor
  -h, --help[=true|false]   help for edit
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc databag item](cinc_databag_item.md)	 - Manage items within a data bag

