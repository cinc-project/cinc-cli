## cinc node run-list list

List a node's run list

```
cinc node run-list list <node> [flags]
```

### Examples

Show a node's current run-list.

```bash
cinc node run-list list web01
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

* [cinc node run-list](cinc_node_run-list.md)	 - List, add, remove, or set a node's run list

