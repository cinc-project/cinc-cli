## cinc role delete

Delete a role from the server

```
cinc role delete <name> [flags]
```

### Examples

Delete a role from the server.

```bash
cinc role delete webserver
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

* [cinc role](cinc_role.md)	 - Manage roles on the Cinc Server

