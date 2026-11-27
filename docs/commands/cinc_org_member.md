## cinc org member

List, add, or remove members of the current org

### Synopsis

Manage the membership of the organization your profile points at.

These verbs are organization-scoped: they act on whichever org the current
profile's server URL targets, not on an org named on the command line. To
manage a different org, switch profiles with --profile.

### Options

```
  -h, --help[=true|false]   help for member
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org](cinc_org.md)	 - Manage organizations on the Cinc Server
* [cinc org member add](cinc_org_member_add.md)	 - Add a user to the current org
* [cinc org member list](cinc_org_member_list.md)	 - List members of the current org
* [cinc org member remove](cinc_org_member_remove.md)	 - Remove a user from the current org

