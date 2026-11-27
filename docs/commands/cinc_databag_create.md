## cinc databag create

Create a data bag, optionally with an initial item

```
cinc databag create <bag> [item] [flags]
```

### Examples

Create an empty data bag.

```bash
cinc databag create passwords
```

Create a data bag and add an item in one step (opens your editor).

```bash
cinc databag create passwords mysql
```

### Options

```
      --file string         read the new item JSON from this file instead of launching the editor (2-arg form only)
  -h, --help[=true|false]   help for create
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc databag](cinc_databag.md)	 - Manage data bags on the Cinc Server

