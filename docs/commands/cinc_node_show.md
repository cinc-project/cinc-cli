## cinc node show

Show a node

```
cinc node show <name> [flags]
```

### Examples

Show a node's headline details: name, platform, run-list, environment, and policy.

```bash
cinc node show web01
```

Show the full node object, including all attributes, as JSON.

```bash
cinc node show web01 --format json
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

* [cinc node](cinc_node.md)	 - Manage nodes on the Cinc Server

