## cinc org

Manage organizations on the Cinc Server

### Synopsis

Manage organizations on the Cinc Server.

The list, show, create, edit, and delete verbs talk to the server root
(/organizations), so they need a pivotal (superuser) identity, the kind of
account that signs the server's own administrative requests. Point a profile at
such an identity to use them.

The member and invite subgroups are organization-scoped: they act on whichever
org the current profile's server URL points at. knife takes the org as an
explicit argument; cinc derives it from your profile instead, so to manage a
different org, switch profiles with --profile.

### Options

```
  -h, --help[=true|false]   help for org
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc](cinc.md)	 - Cinc is a unified command-line tool for Cinc/Chef Infra
* [cinc org acl](cinc_org_acl.md)	 - Manage the ACL of the current organization
* [cinc org create](cinc_org_create.md)	 - Create an organization on the server
* [cinc org delete](cinc_org_delete.md)	 - Delete an organization from the server
* [cinc org edit](cinc_org_edit.md)	 - Edit an organization on the server
* [cinc org invite](cinc_org_invite.md)	 - List, create, or rescind invitations for the current org
* [cinc org list](cinc_org_list.md)	 - List organizations on the server
* [cinc org member](cinc_org_member.md)	 - List, add, or remove members of the current org
* [cinc org show](cinc_org_show.md)	 - Show an organization

