## cinc org invite create

Invite a user to the current org

```
cinc org invite create <user> [flags]
```

### Examples

Invite a user to the org your profile points at.

```bash
cinc org invite create carol
```

### Options

```
  -h, --help[=true|false]   help for create
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org invite](cinc_org_invite.md)	 - List, create, or rescind invitations for the current org

