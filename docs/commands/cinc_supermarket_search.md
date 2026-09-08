## cinc supermarket search

Search cookbooks on Chef Supermarket

### Synopsis

Fuzzy-searches cookbook name, description, and maintainer on
Chef Supermarket. With --verbose the output also includes the
maintainer and latest published version of each hit.

```
cinc supermarket search <query> [flags]
```

### Examples

Search Supermarket for cookbooks.

```bash
cinc supermarket search nginx
```

### Options

```
  -h, --help                      help for search
      --limit int                 cap the number of entries returned (default: all matches)
      --supermarket-site string   URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)
  -v, --verbose                   include maintainer and latest version per cookbook
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc supermarket](cinc_supermarket.md)	 - Manage cookbooks on Chef Supermarket

