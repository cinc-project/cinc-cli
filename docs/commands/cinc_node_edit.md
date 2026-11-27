## cinc node edit

Edit a node on the server

```
cinc node edit <name> [flags]
```

### Examples

Open a node's JSON in your editor and save changes back.

```bash
cinc node edit web01
```

### Options

```
      --file string         read the updated node JSON from this file instead of launching the editor
  -h, --help[=true|false]   help for edit
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node](cinc_node.md)	 - Manage nodes on the Cinc Server

