## cinc role edit

Edit a role on the server

```
cinc role edit <name> [flags]
```

### Examples

Edit a role's run-list and attributes in your editor.

```bash
cinc role edit webserver
```

### Options

```
      --file string         read the updated role JSON from this file instead of launching the editor
  -h, --help[=true|false]   help for edit
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc role](cinc_role.md)	 - Manage roles on the Cinc Server

