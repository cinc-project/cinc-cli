## cinc supermarket download

Download a cookbook tarball from Chef Supermarket

### Synopsis

Downloads a cookbook from Chef Supermarket and writes it to disk
as a gzipped tarball. The version defaults to the latest published
version. By default the tarball lands at ./<cookbook>-<version>.tar.gz;
pass --file to choose a target file or directory.

```
cinc supermarket download <cookbook> [version] [flags]
```

### Examples

Download a cookbook tarball from Supermarket.

```bash
cinc supermarket download nginx
```

### Options

```
      --file string               output file or directory (default: ./<cookbook>-<version>.tar.gz)
      --force                     overwrite the output file if it already exists
  -h, --help                      help for download
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

