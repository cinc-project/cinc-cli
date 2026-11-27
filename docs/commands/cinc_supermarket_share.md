## cinc supermarket share

Share a cookbook on Chef Supermarket

### Synopsis

Packages a local cookbook and uploads it to Chef Supermarket.
Uploads are signed with the profile's identity. By default that's
client_name/client_key, the same identity used against your Cinc
Server. To publish to the public Supermarket under a different
identity, set supermarket_client_name and/or supermarket_key in the
profile; each falls back independently to client_name/client_key
when unset, so you can override just the username, just the key, or
both.

```
cinc supermarket share <cookbook> [category] [flags]
```

### Examples

Share a local cookbook to Supermarket (requires credentials).

```bash
cinc supermarket share nginx 'Web Servers'
```

### Options

```
      --cookbook-path string         directory or path list containing cookbooks (default current directory)
      --dry-run[=true|false]         build the cookbook tarball without uploading it
  -h, --help[=true|false]            help for share
      --no-chefignore[=true|false]   do not exclude files matched by the cookbook's chefignore file
      --supermarket-site string      URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc supermarket](cinc_supermarket.md)	 - Manage cookbooks on Chef Supermarket

