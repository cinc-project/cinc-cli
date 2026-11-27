## cinc policy create

Scaffold a new Policyfile on disk

```
cinc policy create <name> [flags]
```

### Examples

Scaffold a starter Policyfile.rb on disk.

```bash
cinc policy create base
```

### Options

```
      --file string          write the Policyfile to this path instead of ./<name>.rb
      --force[=true|false]   overwrite the file if it already exists
  -h, --help[=true|false]    help for create
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc policy](cinc_policy.md)	 - Manage Policyfile policies on the Cinc Server

