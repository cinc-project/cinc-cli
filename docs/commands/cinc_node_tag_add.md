## cinc node tag add

Add tags to a node

```
cinc node tag add <node> <tag>... [flags]
```

### Examples

Add one or more tags to a node.

```bash
cinc node tag add web01 prod canary
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

* [cinc node tag](cinc_node_tag.md)	 - Add, remove, set, or list a node's tags

