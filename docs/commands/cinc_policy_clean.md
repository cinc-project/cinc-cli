## cinc policy clean

Delete policy revisions that no policy group references

```
cinc policy clean [name] [flags]
```

### Examples

Delete revisions of a policy that no policy group references.

```bash
cinc policy clean appserver
```

### Options

```
      --dry-run[=true|false]   report what would be deleted without deleting anything
  -h, --help[=true|false]      help for clean
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc policy](cinc_policy.md)	 - Manage Policyfile policies on the Cinc Server

