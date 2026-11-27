## cinc user acl

Manage the ACL of a user

### Synopsis

Manage the access-control list (ACL) of this object.

A Chef ACL grants five permissions (create, read, update, delete, and grant)
to actors (users and clients) and to groups. Editing an ACL requires an
identity that already holds the grant permission on the object.

User ACLs are global, not org-scoped: they live at the server root, so they need an identity with grant permission on the user object itself.

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

* [cinc user](cinc_user.md)	 - Manage users on the Cinc Server
* [cinc user acl grant](cinc_user_acl_grant.md)	 - Add members to a permission
* [cinc user acl revoke](cinc_user_acl_revoke.md)	 - Remove members from a permission
* [cinc user acl show](cinc_user_acl_show.md)	 - Show the full ACL

