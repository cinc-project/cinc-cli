## cinc user password

Set a user's password

```
cinc user password <name> [flags]
```

### Examples

Set or reset a user's password (you are prompted if --password is omitted).

```bash
cinc user password alice
```

### Options

```
  -h, --help[=true|false]   help for password
      --password string     the new password (prompted for if omitted)
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc user](cinc_user.md)	 - Manage users on the Cinc Server

