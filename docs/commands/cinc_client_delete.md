## cinc client delete

Delete an API client from the server

```
cinc client delete <name> [flags]
```

### Examples

Delete an API client from the server.

```bash
cinc client delete worker-01
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

* [cinc client](cinc_client.md)	 - Manage API clients on the Cinc Server

