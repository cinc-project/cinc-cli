## cinc node tag remove

Remove tags from a node

```
cinc node tag remove <node> <tag>... [flags]
```

### Examples

Remove a tag from a node.

```bash
cinc node tag remove web01 canary
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

* [cinc node tag](cinc_node_tag.md)	 - Add, remove, set, or list a node's tags

