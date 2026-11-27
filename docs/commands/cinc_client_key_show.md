## cinc client key show

Show one of a client's keys

```
cinc client key show <client> <key-name> [flags]
```

### Examples

Show one of a client's keys.

```bash
cinc client key show worker-01 default
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

* [cinc client key](cinc_client_key.md)	 - Manage a client's public keys

