## cinc org acl show

Show the full ACL

### Synopsis

Show all five permissions of the ACL and the actors and groups each grants.

```
cinc org acl show [flags]
```

### Examples

Show the current organization's own ACL.

```bash
cinc org acl show
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

* [cinc org acl](cinc_org_acl.md)	 - Manage the ACL of the current organization

