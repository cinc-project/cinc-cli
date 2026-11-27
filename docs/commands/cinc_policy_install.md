## cinc policy install

Resolve a Policyfile.rb and write a push-ready lock

### Synopsis

Resolve a Policyfile.rb into a push-ready Policyfile.lock.json.

cinc runs your Policyfile through an embedded CRuby engine (CRuby
compiled to WebAssembly, run with no system Ruby and no CGo), so any
valid Ruby works: loops, conditionals, helper methods, ENV, string
interpolation, and require_relative of sibling files all behave just
as they do with chef. The first run downloads a pinned ruby.wasm and
caches it; later runs are offline.

cinc then resolves your cookbooks: it reads each cookbook's metadata,
solves versions against every `depends` and the constraints in your
Policyfile, and computes the same content identifiers chef does. The
resulting Policyfile.lock.json is byte-for-byte compatible with what
`chef install` writes, so you can `cinc policy push` it straight to a
Cinc/Chef Infra Server.

Today path: cookbooks are the fully supported source. git:,
Supermarket, and chef server sources aren't resolved yet.

```
cinc policy install [Policyfile.rb] [flags]
```

### Examples

Resolve ./Policyfile.rb and write ./Policyfile.lock.json.

```bash
cinc policy install
```

Resolve a specific Policyfile and print a summary as JSON.

```bash
cinc policy install path/to/Policyfile.rb --format json
```

### Options

```
  -h, --help[=true|false]   help for install
      --output string       write the lock to this path instead of ./Policyfile.lock.json
```

### Options inherited from parent commands

```
      --config string    path to the Cinc credentials file (default ~/.cinc/credentials)
      --format string    output format: human or json (default "human")
      --profile string   credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then "default")
```

### SEE ALSO

* [cinc policy](cinc_policy.md)	 - Manage Policyfile policies on the Cinc Server

