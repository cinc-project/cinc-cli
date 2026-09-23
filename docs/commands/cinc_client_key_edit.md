## cinc client key edit

Edit one of a client's keys

```
cinc client key edit <client> <key-name> [flags]
```

### Examples

Edit one of a client's keys, for example its expiration.

```bash
cinc client key edit worker-01 default
```

Have the server regenerate a key (regenerate.json holds {"create_key": true}) and save the new private key.

```bash
cinc client key edit worker-01 rotation --file regenerate.json --key-file rotation.pem
```

### Options

```
      --file string       read the updated key JSON from this file instead of launching the editor
  -h, --help              help for edit
      --key-file string   write a regenerated private key (from "create_key": true) to this file instead of stdout
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc client key](cinc_client_key.md)	 - Manage a client's public keys

