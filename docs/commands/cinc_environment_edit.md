## cinc environment edit

Edit an environment on the server

```
cinc environment edit <name> [flags]
```

### Examples

Edit an environment's cookbook version constraints and attributes.

```bash
cinc environment edit prod
```

### Options

```
      --file string         read the updated environment JSON from this file instead of launching the editor
  -h, --help[=true|false]   help for edit
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc environment](cinc_environment.md)	 - Manage environments on the Cinc Server

