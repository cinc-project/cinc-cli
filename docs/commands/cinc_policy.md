## cinc policy

Manage Policyfile policies on the Cinc Server

### Options

```
  -h, --help[=true|false]   help for policy
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc](cinc.md)	 - Cinc is a unified command-line tool for Cinc/Chef Infra
* [cinc policy acl](cinc_policy_acl.md)	 - Manage the ACL of a policy
* [cinc policy clean](cinc_policy_clean.md)	 - Delete policy revisions that no policy group references
* [cinc policy clean-cookbooks](cinc_policy_clean-cookbooks.md)	 - Delete cookbook artifacts that no policy revision references
* [cinc policy create](cinc_policy_create.md)	 - Scaffold a new Policyfile on disk
* [cinc policy delete](cinc_policy_delete.md)	 - Delete a policy and all its revisions from the server
* [cinc policy diff](cinc_policy_diff.md)	 - Compare two revisions of a policy
* [cinc policy export](cinc_policy_export.md)	 - Assemble a standalone bundle from a Policyfile lock
* [cinc policy install](cinc_policy_install.md)	 - Resolve a Policyfile.rb and write a push-ready lock
* [cinc policy list](cinc_policy_list.md)	 - List policies on the server
* [cinc policy push](cinc_policy_push.md)	 - Deploy a Policyfile lock to a policy group
* [cinc policy push-archive](cinc_policy_push-archive.md)	 - Deploy an exported Policyfile bundle to a policy group
* [cinc policy show](cinc_policy_show.md)	 - Show a policy's revisions

