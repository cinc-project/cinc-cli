## cinc org create

Create an organization on the server

### Synopsis

Create an organization on the server.

The server provisions a validator client and returns its private key exactly
once, right here. Capture it now; it can't be retrieved later. Use --filename
to write it straight to disk.

This hits the server root, so it needs a pivotal (superuser) identity.

```
cinc org create <shortname> <fullname> [flags]
```

### Examples

Create an org and write its validator key to a file.

```bash
cinc org create acme "Acme Corporation" --filename acme-validator.pem
```

Create an org and stream the validator key to stdout.

```bash
cinc org create acme "Acme Corporation"
```

### Options

```
  -f, --filename string     write the generated validator private key to this file instead of stdout
  -h, --help[=true|false]   help for create
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org](cinc_org.md)	 - Manage organizations on the Cinc Server

