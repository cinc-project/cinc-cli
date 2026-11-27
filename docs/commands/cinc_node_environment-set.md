## cinc node environment-set

Set a node's environment

```
cinc node environment-set <node> <environment> [flags]
```

### Examples

Move a node into a different environment.

```bash
cinc node environment-set web01 prod
```

### Options

```
  -h, --help[=true|false]   help for environment-set
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node](cinc_node.md)	 - Manage nodes on the Cinc Server

