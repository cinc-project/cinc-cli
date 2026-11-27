## cinc policy-group show

Show a policy group's active policy revisions

```
cinc policy-group show <name> [flags]
```

### Examples

Show the policies and revisions active in a group.

```bash
cinc policy-group show prod
```

### Options

```
  -h, --help[=true|false]   help for show
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc policy-group](cinc_policy-group.md)	 - Manage policy groups on the Cinc Server

