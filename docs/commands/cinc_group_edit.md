## cinc group edit

Edit a group's members on the server

```
cinc group edit <name> [flags]
```

### Examples

Edit a group's membership in your editor.

```bash
cinc group edit admins
```

### Options

```
      --file string         read the updated group JSON from this file instead of launching the editor
  -h, --help[=true|false]   help for edit
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc group](cinc_group.md)	 - Manage groups on the Cinc Server

