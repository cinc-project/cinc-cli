## cinc client show

Show an API client

```
cinc client show <name> [flags]
```

### Examples

Show an API client, including whether it is a validator.

```bash
cinc client show worker-01
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

* [cinc client](cinc_client.md)	 - Manage API clients on the Cinc Server

