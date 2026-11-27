## cinc cookbook list

List cookbooks on the server

```
cinc cookbook list [flags]
```

### Examples

List every cookbook on the server.

```bash
cinc cookbook list
```

### Options

```
  -h, --help[=true|false]   help for list
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc cookbook](cinc_cookbook.md)	 - Manage cookbooks on the Cinc Server

