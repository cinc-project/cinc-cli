## cinc node policy-set

Set a node's policy group and policy name

```
cinc node policy-set <node> <policy-group> <policy-name> [flags]
```

### Examples

Switch a node to Policyfile-based management.

```bash
cinc node policy-set web01 prod base
```

### Options

```
  -h, --help[=true|false]   help for policy-set
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node](cinc_node.md)	 - Manage nodes on the Cinc Server

