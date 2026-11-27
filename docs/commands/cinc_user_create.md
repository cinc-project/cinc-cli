## cinc user create

Create a user on the server

```
cinc user create <name> [flags]
```

### Examples

Create a user; the server generates the key, written to a file.

```bash
cinc user create alice --email alice@example.com --first-name Alice --last-name Smith --key-file alice.pem
```

Register a public key you already have; the server generates no key.

```bash
cinc user create alice --email alice@example.com --public-key alice.pub
```

### Options

```
      --display-name string   user's display name
      --email string          user's email address
      --first-name string     user's first name
  -h, --help[=true|false]     help for create
  -f, --key-file string       write the generated private key to this file instead of stdout
      --last-name string      user's last name
      --middle-name string    user's middle name
      --password string       user's initial password
      --public-key string     path to a PEM public key; the server will not generate a key pair
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc user](cinc_user.md)	 - Manage users on the Cinc Server

