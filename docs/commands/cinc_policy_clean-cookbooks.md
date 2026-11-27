## cinc policy clean-cookbooks

Delete cookbook artifacts that no policy revision references

### Synopsis

Delete cookbook artifacts (/cookbook_artifacts/NAME/IDENTIFIER) that no
policy revision references.

Tip: run `cinc policy clean` first. Pruning unreferenced revisions can
orphan more cookbook artifacts, which this command will then clean up.

```
cinc policy clean-cookbooks [flags]
```

### Examples

Delete cookbook artifacts no policy revision uses.

```bash
cinc policy clean-cookbooks
```

Preview what would be deleted without deleting anything.

```bash
cinc policy clean-cookbooks --dry-run
```

### Options

```
      --dry-run[=true|false]   report what would be deleted without deleting anything
  -h, --help[=true|false]      help for clean-cookbooks
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc policy](cinc_policy.md)	 - Manage Policyfile policies on the Cinc Server

