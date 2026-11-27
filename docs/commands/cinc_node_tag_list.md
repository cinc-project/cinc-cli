## cinc node tag list

List a node's tags

```
cinc node tag list <node> [flags]
```

### Examples

Show a node's current tags.

```bash
cinc node tag list web01
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

* [cinc node tag](cinc_node_tag.md)	 - Add, remove, set, or list a node's tags

