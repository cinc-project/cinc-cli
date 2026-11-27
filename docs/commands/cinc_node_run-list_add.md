## cinc node run-list add

Append entries to a node's run list

```
cinc node run-list add <node> <entry>... [flags]
```

### Examples

Append an entry to a node's run-list (existing entries are kept).

```bash
cinc node run-list add web01 'recipe[ntp]'
```

### Options

```
  -h, --help[=true|false]   help for add
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node run-list](cinc_node_run-list.md)	 - List, add, remove, or set a node's run list

