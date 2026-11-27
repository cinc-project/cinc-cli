## cinc databag secret

Manage encrypted data bag items

### Synopsis

Manage Chef-compatible encrypted data bag items.

Values are encrypted client-side with a shared secret before upload and
decrypted on read; the item "id" stays in cleartext. Provide the secret
with --secret-file, --secret, $CINC_SECRET_FILE, or a secret_file key in
your profile.

To list or delete encrypted items use the regular "cinc databag item list"
and "cinc databag item delete" commands; neither needs the secret.

### Options

```
  -h, --help[=true|false]   help for secret
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc databag](cinc_databag.md)	 - Manage data bags on the Cinc Server
* [cinc databag secret create](cinc_databag_secret_create.md)	 - Create an encrypted item in a data bag
* [cinc databag secret edit](cinc_databag_secret_edit.md)	 - Edit an encrypted data bag item
* [cinc databag secret show](cinc_databag_secret_show.md)	 - Show and decrypt an encrypted data bag item

