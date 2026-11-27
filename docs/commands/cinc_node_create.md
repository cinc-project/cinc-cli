## cinc node create

Create a node on the server

```
cinc node create <name> [flags]
```

### Examples

Create a node with a starting environment and run-list.

```bash
cinc node create web01 --environment prod --run-list 'recipe[base],role[web]'
```

Create a Policyfile-managed node pinned to a policy group.

```bash
cinc node create web01 --policy-group prod --policy-name base
```

Create a node from a JSON file.

```bash
cinc node create web01 --file web01.json
```

### Options

```
  -E, --environment string    chef_environment for the new node
      --file string           read the full node JSON from this file instead of using flags
  -h, --help[=true|false]     help for create
      --policy-group string   policy group for a Policyfile-managed node (requires --policy-name)
      --policy-name string    policy name for a Policyfile-managed node (requires --policy-group)
      --run-list string       comma-separated run list for the new node
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node](cinc_node.md)	 - Manage nodes on the Cinc Server

