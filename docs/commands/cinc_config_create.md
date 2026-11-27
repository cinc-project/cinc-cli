## cinc config create

Create or update a local credentials profile

```
cinc config create [flags]
```

### Examples

Create or update the default credentials profile interactively.

```bash
cinc config create
```

### Options

```
      --chef-server-url string    Cinc Server URL including /organizations/<org>
      --cinc-server-url string    Cinc Server URL including /organizations/<org>
      --client-key string         path to the PEM private key for the client
      --client-name string        client name used to sign API requests
  -h, --help[=true|false]         help for create
      --server-url string         Cinc Server URL including /organizations/<org>
      --ssl-verify-mode string    optional SSL verify mode such as :verify_peer or :verify_none
      --supermarket-site string   Chef Supermarket URL for cookbook uploads
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc config](cinc_config.md)	 - Manage local Cinc configuration

