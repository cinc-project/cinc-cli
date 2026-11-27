## cinc search

Search the Cinc Server

### Synopsis

Search the Cinc Server's index for objects matching a Solr/Lucene query.

The index is one of node, role, environment, client, or a data bag name.
By default matches render as an aligned table; -a projects specific
attributes into columns, -i prints just the names, and --format json
emits the full result.

```
cinc search <index> <query> [flags]
```

### Examples

Search an index: node, role, environment, client, or a data bag name.

```bash
cinc search node 'role:web'
```

### Options

```
  -a, --attribute stringArray   return only this attribute (repeatable); the requested attributes become the columns
  -h, --help[=true|false]       help for search
  -i, --id-only[=true|false]    print only the matching object names/ids, one per line
      --rows int                maximum number of results to return (0 returns all matches)
      --start int               offset of the first result to return
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc](cinc.md)	 - Cinc is a unified command-line tool for Cinc/Chef Infra

