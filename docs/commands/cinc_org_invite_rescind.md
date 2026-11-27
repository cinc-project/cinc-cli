## cinc org invite rescind

Cancel a pending invitation for the current org

### Synopsis

Cancel a pending invitation by its id.

Run `cinc org invite list` to find the id of the invitation you want to cancel.

```
cinc org invite rescind <id> [flags]
```

### Examples

Cancel a pending invitation by its id.

```bash
cinc org invite rescind acme-carol
```

### Options

```
  -h, --help[=true|false]   help for rescind
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org invite](cinc_org_invite.md)	 - List, create, or rescind invitations for the current org

