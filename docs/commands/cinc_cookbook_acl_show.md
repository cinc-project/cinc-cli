## cinc cookbook acl show

Show the full ACL

### Synopsis

Show all five permissions of the ACL and the actors and groups each grants.

```
cinc cookbook acl show <name> [flags]
```

### Examples

Show the full ACL of cookbook nginx.

```bash
cinc cookbook acl show nginx
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

* [cinc cookbook acl](cinc_cookbook_acl.md)	 - Manage the ACL of a cookbook

