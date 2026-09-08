## cinc supermarket list

List cookbooks on Chef Supermarket

### Synopsis

Lists every cookbook on Chef Supermarket. With --verbose the
output also includes the maintainer and latest published version
of each cookbook (one extra request to /universe, fast).

```
cinc supermarket list [flags]
```

### Examples

List cookbooks available on Chef Supermarket.

```bash
cinc supermarket list
```

### Options

```
  -h, --help                      help for list
      --limit int                 cap the number of entries returned (default: all)
      --order string              sort order: recently_updated, recently_added, most_downloaded, most_followed
      --supermarket-site string   URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)
      --user string               only show cookbooks owned by this Supermarket username
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

