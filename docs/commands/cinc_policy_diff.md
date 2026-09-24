## cinc policy diff

Compare two revisions of a policy

### Synopsis

Compare two revisions of a policy.

By default ref1 and ref2 name policy groups and the comparison is
between the revision active in each. Pass --revisions to treat them as
revision ids instead: cinc policy diff NAME --revisions A B.

```
cinc policy diff <name> <ref1> <ref2> [flags]
```

### Examples

Compare the appserver revisions active in the staging and production groups.

```bash
cinc policy diff appserver staging production
```

Compare two revisions by their revision ids.

```bash
cinc policy diff appserver --revisions 1a2b3c4d 5e6f7a8b
```

### Options

```
  -h, --help        help for diff
      --revisions   treat the two refs as revision ids rather than policy group names
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc policy](cinc_policy.md)	 - Manage Policyfile policies on the Cinc Server

