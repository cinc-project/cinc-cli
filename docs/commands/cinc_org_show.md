## cinc org show

Show an organization

### Synopsis

Show an organization's metadata.

This hits the server root, so it needs a pivotal (superuser) identity.

```
cinc org show <org> [flags]
```

### Examples

Show an organization's metadata.

```bash
cinc org show acme
```

### Options

```
  -h, --help[=true|false]   help for show
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org](cinc_org.md)	 - Manage organizations on the Cinc Server

