## cinc client key create

Add a key to a client

```
cinc client key create <client> <key-name> [flags]
```

### Examples

Have the server generate the key pair and write the private key to a file.

```bash
cinc client key create worker-01 rotation --key-file rotation.pem
```

Register a public key you already have; the server generates nothing.

```bash
cinc client key create worker-01 laptop --public-key ~/.ssh/id_rsa.pub
```

Add a key that expires on a given date.

```bash
cinc client key create worker-01 temp --expires 2030-01-01T00:00:00Z
```

### Options

```
      --expires string      expiration date (ISO-8601 UTC) or 'infinity', the default
  -h, --help                help for create
  -f, --key-file string     write the generated private key to this file instead of stdout
      --public-key string   path to a PEM public key; the server will not generate a key pair
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc client key](cinc_client_key.md)	 - Manage a client's public keys

