## cinc user edit

Edit a user on the server

```
cinc user edit <name> [flags]
```

### Examples

Open a user's JSON in your editor and save it back.

```bash
cinc user edit alice
```

### Options

```
      --file string         read the updated user JSON from this file instead of launching the editor
  -h, --help[=true|false]   help for edit
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc user](cinc_user.md)	 - Manage users on the Cinc Server

