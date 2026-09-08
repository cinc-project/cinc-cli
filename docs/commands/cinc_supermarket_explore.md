## cinc supermarket explore

Browse Chef Supermarket cookbooks in a terminal UI

### Synopsis

Launches an interactive terminal UI for browsing cookbooks on Chef
Supermarket. Move with arrow keys, press / to search, press d/u/a to
sort by Downloads, Updated, or Alphabetical, enter for full details,
i to install the highlighted cookbook onto your Cinc Server, and q to
quit.

```
cinc supermarket explore [flags]
```

### Examples

Browse Supermarket cookbooks in an interactive terminal UI.

```bash
cinc supermarket explore
```

### Options

```
  -h, --help                      help for explore
      --supermarket-site string   URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc supermarket](cinc_supermarket.md)	 - Manage cookbooks on Chef Supermarket

