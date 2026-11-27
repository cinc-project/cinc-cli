## cinc explore

Browse and edit the Cinc Server in a terminal UI

### Synopsis

Launches an interactive, k9s-style terminal UI for the whole Cinc
Server. Pick a profile (when more than one is configured), choose an
object type, and browse, view, edit, create, delete, or download
objects from a contextual action bar.

Move with the arrow keys, / to filter the loaded list, s to run a
server-side search, : for the object-type menu, enter to open or
drill in, esc to go back, and q to quit.

```
cinc explore [flags]
```

### Examples

Launch the interactive terminal UI to browse nodes, roles, and more.

```bash
cinc explore
```

### Options

```
  -h, --help[=true|false]   help for explore
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc](cinc.md)	 - Cinc is a unified command-line tool for Cinc/Chef Infra

