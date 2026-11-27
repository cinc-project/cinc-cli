## cinc org acl

Manage the ACL of the current organization

### Synopsis

Manage the access-control list (ACL) of this object.

A Chef ACL grants five permissions (create, read, update, delete, and grant)
to actors (users and clients) and to groups. Editing an ACL requires an
identity that already holds the grant permission on the object.

The org ACL is the organization object's own ACL. It applies to whichever org the current profile's server URL points at; switch --profile to manage another.

### Options

```
  -h, --help[=true|false]   help for acl
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org](cinc_org.md)	 - Manage organizations on the Cinc Server
* [cinc org acl grant](cinc_org_acl_grant.md)	 - Add members to a permission
* [cinc org acl revoke](cinc_org_acl_revoke.md)	 - Remove members from a permission
* [cinc org acl show](cinc_org_acl_show.md)	 - Show the full ACL

