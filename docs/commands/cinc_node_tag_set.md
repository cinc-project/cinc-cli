## cinc node tag set

Replace a node's tags

```
cinc node tag set <node> <tag>... [flags]
```

### Examples

Replace a node's tags entirely.

```bash
cinc node tag set web01 prod web
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

* [cinc node tag](cinc_node_tag.md)	 - Add, remove, set, or list a node's tags

