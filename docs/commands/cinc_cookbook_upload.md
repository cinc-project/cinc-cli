## cinc cookbook upload

Upload cookbook versions to the Cinc Server

```
cinc cookbook upload <name>... [flags]
```

### Examples

Upload a cookbook from your cookbook path.

```bash
cinc cookbook upload nginx
```

### Options

```
      --cookbook-path string   directory or path list containing cookbooks (default current directory)
  -h, --help[=true|false]      help for upload
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc cookbook](cinc_cookbook.md)	 - Manage cookbooks on the Cinc Server

