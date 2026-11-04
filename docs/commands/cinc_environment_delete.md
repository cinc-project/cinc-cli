## cinc environment delete

Delete an environment from the server

```
cinc environment delete <name> [flags]
```

### Examples

Delete an environment from the server.

```bash
cinc environment delete prod
```

### Options

```
  -h, --help   help for delete
  -y, --yes    accepted for knife compatibility; this command doesn't ask before deleting
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc environment](cinc_environment.md)	 - Manage environments on the Cinc Server

