## cinc user delete

Delete a user from the server

```
cinc user delete <name> [flags]
```

### Examples

Delete a user from the server.

```bash
cinc user delete alice
```

### Options

```
  -h, --help[=true|false]   help for delete
  -y, --yes[=true|false]    skip the confirmation prompt when deleting the pivotal superuser
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc user](cinc_user.md)	 - Manage users on the Cinc Server

