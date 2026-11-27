## cinc client edit

Edit an API client on the server

```
cinc client edit <name> [flags]
```

### Examples

Open an API client's JSON in your editor and save it back.

```bash
cinc client edit worker-01
```

### Options

```
      --file string         read the updated client JSON from this file instead of launching the form
  -h, --help[=true|false]   help for edit
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc client](cinc_client.md)	 - Manage API clients on the Cinc Server

