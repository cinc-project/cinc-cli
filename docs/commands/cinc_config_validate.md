## cinc config validate

Validate local Cinc TOML configuration and endpoint reachability

```
cinc config validate [path] [flags]
```

### Examples

Run the pre-flight checks for every profile in your credentials file.

```bash
cinc config validate
```

Check just one profile.

```bash
cinc config validate --profile staging
```

### Options

```
  -h, --help   help for validate
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc config](cinc_config.md)	 - Manage local Cinc configuration

