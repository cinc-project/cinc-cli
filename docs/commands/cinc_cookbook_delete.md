## cinc cookbook delete

Delete a cookbook version from the server

```
cinc cookbook delete <name> <version> [flags]
```

### Examples

Delete a specific cookbook version from the server.

```bash
cinc cookbook delete nginx 1.2.0
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

* [cinc cookbook](cinc_cookbook.md)	 - Manage cookbooks on the Cinc Server

