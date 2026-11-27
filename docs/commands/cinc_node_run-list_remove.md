## cinc node run-list remove

Remove entries from a node's run list

```
cinc node run-list remove <node> <entry>... [flags]
```

### Examples

Remove an entry from a node's run-list.

```bash
cinc node run-list remove web01 'recipe[ntp]'
```

### Options

```
  -h, --help[=true|false]   help for remove
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node run-list](cinc_node_run-list.md)	 - List, add, remove, or set a node's run list

