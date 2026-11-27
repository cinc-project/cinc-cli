## cinc org edit

Edit an organization on the server

### Synopsis

Edit an organization, typically to change its full name.

Fetches the org, opens its JSON in your editor, and saves the result back. Use
--file to supply the JSON non-interactively.

This hits the server root, so it needs a pivotal (superuser) identity.

```
cinc org edit <org> [flags]
```

### Examples

Open an org's JSON in your editor and save it back.

```bash
cinc org edit acme
```

### Options

```
      --file string         read the updated org JSON from this file instead of launching the editor
  -h, --help[=true|false]   help for edit
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org](cinc_org.md)	 - Manage organizations on the Cinc Server

