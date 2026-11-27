## cinc node status

Show nodes and when they last checked in

```
cinc node status [query] [flags]
```

### Examples

Show every node with how long ago it last checked in.

```bash
cinc node status
```

Limit the report to nodes matching a search query.

```bash
cinc node status 'role:web'
```

### Options

```
  -h, --help[=true|false]   help for status
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node](cinc_node.md)	 - Manage nodes on the Cinc Server

