## cinc client create

Create an API client on the server

```
cinc client create <name> [flags]
```

### Examples

Create a client; the server generates the key, written to a file.

```bash
cinc client create worker-01 --key-file worker-01.pem
```

Create a validator client, used to bootstrap new nodes.

```bash
cinc client create bootstrap-validator --validator --key-file validator.pem
```

Register a public key you already have; the server generates no key.

```bash
cinc client create worker-01 --public-key worker-01.pub
```

### Options

```
  -h, --help[=true|false]        help for create
  -f, --key-file string          write the generated private key to this file instead of stdout
      --public-key string        path to a PEM public key; the server will not generate a key pair
      --validator[=true|false]   create a validator client
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc client](cinc_client.md)	 - Manage API clients on the Cinc Server

