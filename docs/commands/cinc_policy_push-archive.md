## cinc policy push-archive

Deploy an exported Policyfile bundle to a policy group

### Synopsis

Deploy a bundle produced by `cinc policy export` to a policy group.

The archive may be a .tar.gz (as written by `cinc policy export --archive`)
or an already-extracted bundle directory. When you don't name one, cinc
looks in the current directory for an extracted bundle (a
Policyfile.lock.json beside you) and then for a single .tar.gz archive.

```
cinc policy push-archive <group> [archive] [flags]
```

### Examples

Deploy a previously exported bundle archive to a policy group.

```bash
cinc policy push-archive prod appserver.tar.gz
```

Deploy an extracted bundle directory.

```bash
cinc policy push-archive prod ./appserver
```

### Options

```
  -h, --help[=true|false]   help for push-archive
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc policy](cinc_policy.md)	 - Manage Policyfile policies on the Cinc Server

