## cinc org delete

Delete an organization from the server

### Synopsis

Delete an organization from the server.

This hits the server root, so it needs a pivotal (superuser) identity.

```
cinc org delete <org> [flags]
```

### Examples

Delete an organization from the server.

```bash
cinc org delete acme
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

* [cinc org](cinc_org.md)	 - Manage organizations on the Cinc Server

