## cinc node run-list set

Replace a node's run list

```
cinc node run-list set <node> <entry>... [flags]
```

### Examples

Replace a node's run-list entirely.

```bash
cinc node run-list set web01 'recipe[base],role[web]'
```

### Options

```
  -h, --help[=true|false]   help for set
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node run-list](cinc_node_run-list.md)	 - List, add, remove, or set a node's run list

