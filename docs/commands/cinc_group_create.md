## cinc group create

Create a group on the server

```
cinc group create <name> [flags]
```

### Examples

Create a group.

```bash
cinc group create admins
```

### Options

```
  -h, --help[=true|false]   help for create
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc group](cinc_group.md)	 - Manage groups on the Cinc Server

