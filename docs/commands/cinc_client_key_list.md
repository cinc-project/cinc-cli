## cinc client key list

List a client's keys

```
cinc client key list <client> [flags]
```

### Examples

List a client's keys.

```bash
cinc client key list worker-01
```

### Options

```
  -h, --help[=true|false]   help for list
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc client key](cinc_client_key.md)	 - Manage a client's public keys

