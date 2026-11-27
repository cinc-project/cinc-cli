## cinc databag secret show

Show and decrypt an encrypted data bag item

```
cinc databag secret show <bag> <id> [flags]
```

### Examples

Show an encrypted data bag item, decrypted.

```bash
cinc databag secret show passwords mysql --secret-file ~/.cinc/secret
```

### Options

```
  -h, --help[=true|false]    help for show
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

