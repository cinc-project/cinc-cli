## cinc node delete

Delete a node from the server

```
cinc node delete <name> [flags]
```

### Examples

Delete a node from the server.

```bash
cinc node delete web01
```

### Options

```
  -h, --help   help for delete
  -y, --yes    accepted for knife compatibility; this command doesn't ask before deleting
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc node](cinc_node.md)	 - Manage nodes on the Cinc Server

