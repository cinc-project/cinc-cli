## cinc databag secret edit

Edit an encrypted data bag item

```
cinc databag secret edit <bag> <id> [flags]
```

### Examples

Edit an encrypted data bag item in your editor.

```bash
cinc databag secret edit passwords mysql --secret-file ~/.cinc/secret
```

### Options

```
      --file string          read the updated item JSON from this file instead of launching the editor
  -h, --help[=true|false]    help for edit
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

