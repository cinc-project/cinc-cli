## cinc org invite

List, create, or rescind invitations for the current org

### Synopsis

Manage pending invitations for the organization your profile points at.

Like the member subgroup, these verbs are organization-scoped: they act on
whichever org the current profile's server URL targets. To manage a different
org, switch profiles with --profile.

### Options

```
  -h, --help[=true|false]   help for invite
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org](cinc_org.md)	 - Manage organizations on the Cinc Server
* [cinc org invite create](cinc_org_invite_create.md)	 - Invite a user to the current org
* [cinc org invite list](cinc_org_invite_list.md)	 - List pending invitations for the current org
* [cinc org invite rescind](cinc_org_invite_rescind.md)	 - Cancel a pending invitation for the current org

