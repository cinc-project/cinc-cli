## cinc supermarket install

Install a Supermarket cookbook onto the Cinc Server

### Synopsis

Downloads a cookbook from Chef Supermarket and uploads it to your
configured Cinc Server in one step. The version defaults to the
latest published version. Only the named cookbook is installed;
its dependencies are not resolved.

```
cinc supermarket install <cookbook> [version] [flags]
```

### Examples

Install the latest version of a cookbook from Supermarket onto the server.

```bash
cinc supermarket install nginx
```

Install a specific version.

```bash
cinc supermarket install nginx 1.2.0
```

### Options

```
  -h, --help                      help for install
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

