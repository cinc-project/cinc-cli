## cinc user key create

Add a key to a user

```
cinc user key create <user> <key-name> [flags]
```

### Examples

Have the server generate the key pair and write the private key to a file.

```bash
cinc user key create alice rotation --key-file rotation.pem
```

Register a public key you already have; the server generates nothing.

```bash
cinc user key create alice laptop --public-key ~/.ssh/id_rsa.pub
```

Add a key that expires on a given date.

```bash
cinc user key create alice temp --expires 2030-01-01T00:00:00Z
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

* [cinc user key](cinc_user_key.md)	 - Manage a user's public keys

