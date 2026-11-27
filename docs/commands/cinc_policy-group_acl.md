## cinc policy-group acl

Manage the ACL of a policy-group

### Synopsis

Manage the access-control list (ACL) of this object.

A Chef ACL grants five permissions (create, read, update, delete, and grant)
to actors (users and clients) and to groups. Editing an ACL requires an
identity that already holds the grant permission on the object.

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

* [cinc policy-group](cinc_policy-group.md)	 - Manage policy groups on the Cinc Server
* [cinc policy-group acl grant](cinc_policy-group_acl_grant.md)	 - Add members to a permission
* [cinc policy-group acl revoke](cinc_policy-group_acl_revoke.md)	 - Remove members from a permission
* [cinc policy-group acl show](cinc_policy-group_acl_show.md)	 - Show the full ACL

