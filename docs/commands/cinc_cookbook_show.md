## cinc cookbook show

Show a cookbook version manifest

```
cinc cookbook show <name> [version] [flags]
```

### Examples

Show the latest version's file manifest.

```bash
cinc cookbook show nginx
```

Show a specific version.

```bash
cinc cookbook show nginx 1.2.0
```

### Options

```
  -h, --help[=true|false]   help for show
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc cookbook](cinc_cookbook.md)	 - Manage cookbooks on the Cinc Server

