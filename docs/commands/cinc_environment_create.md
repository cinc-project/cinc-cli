## cinc environment create

Create an environment on the server

```
cinc environment create <name> [flags]
```

### Examples

Create an empty environment with a description.

```bash
cinc environment create prod --description "Production"
```

Create an environment with its cookbook constraints and attributes from a JSON file.

```bash
cinc environment create prod --file prod.json
```

### Options

```
  -d, --description string   human-readable description for the new environment
      --file string          read the full environment JSON from this file instead of using flags
  -h, --help                 help for create
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc environment](cinc_environment.md)	 - Manage environments on the Cinc Server

