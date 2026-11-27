## cinc org member add

Add a user to the current org

### Synopsis

Immediately associate an existing user with the current org.

This is a superuser operation; if you're not pivotal, use `cinc org invite create` to
send an invitation the user accepts instead.

```
cinc org member add <user> [flags]
```

### Examples

Add an existing user to the org your profile points at.

```bash
cinc org member add alice
```

### Options

```
  -h, --help[=true|false]   help for add
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org member](cinc_org_member.md)	 - List, add, or remove members of the current org

