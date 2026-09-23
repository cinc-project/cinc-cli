## cinc org list

List organizations on the server

### Synopsis

List every organization on the server.

Listing every org needs a pivotal (superuser) identity. If the server won't
let you, you get the organizations your user belongs to instead, with a note
on stderr saying so.

```
cinc org list [flags]
```

### Examples

List every organization on the server.

```bash
cinc org list
```

### Options

```
  -h, --help   help for list
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc org](cinc_org.md)	 - Manage organizations on the Cinc Server

