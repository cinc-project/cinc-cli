## cinc databag secret create

Create an encrypted item in a data bag

```
cinc databag secret create <bag> <id> [flags]
```

### Examples

Create an encrypted item; your editor opens to edit its plaintext JSON.

```bash
cinc databag secret create passwords mysql --secret-file ~/.cinc/secret
```

### Options

```
      --file string          read the new item JSON from this file instead of launching the editor
  -h, --help[=true|false]    help for create
      --secret string        the encrypted data bag secret as a literal string (mutually exclusive with --secret-file)
      --secret-file string   path to the encrypted data bag secret file
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc databag secret](cinc_databag_secret.md)	 - Manage encrypted data bag items

