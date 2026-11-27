## cinc policy export

Assemble a standalone bundle from a Policyfile lock

```
cinc policy export [lock] [dir] [flags]
```

### Examples

Assemble a standalone bundle for an air-gapped cinc-client -z run.

```bash
cinc policy export Policyfile.lock.json ./bundle --archive
```

### Options

```
  -a, --archive[=true|false]   also write the bundle as a .tar.gz archive
  -h, --help[=true|false]      help for export
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc policy](cinc_policy.md)	 - Manage Policyfile policies on the Cinc Server

