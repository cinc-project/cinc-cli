## cinc role create

Create a role on the server

```
cinc role create <name> [flags]
```

### Examples

Create an empty role with a description.

```bash
cinc role create webserver --description "Front-end web servers"
```

Create a role with its run list and attributes from a JSON file.

```bash
cinc role create webserver --file webserver.json
```

### Options

```
  -d, --description string   human-readable description for the new role
      --file string          read the full role JSON from this file instead of using flags
  -h, --help                 help for create
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc role](cinc_role.md)	 - Manage roles on the Cinc Server

