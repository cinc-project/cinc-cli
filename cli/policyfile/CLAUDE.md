# cli/policyfile

## Gotchas

- **`CookbookLock.Origin()` picks by key precedence, not by intent.** `cinc-api`
  checks `source_options` in the order path, artifactserver, git, chef_server,
  and returns the first hit (the call site is `fetch.go`). A lock carrying both
  a repository URL and a `path` is therefore classified as a *path* source, and
  the repository fetch never runs. Reason about which branch actually executes
  before concluding a code path is reachable.
- **A cookbook's files come from cinc-api.** `copyCookbook` (export) and the
  resolver's identifiers use `cinc.LocalCookbookFromDir`'s `Files()` and
  `Identifiers()`, the same set an upload sends. `copyTree` is only for
  caching a fetched source tree as-is. See `cli/cookbook/CLAUDE.md`.

Tarball extraction (and its caps) lives in `cli/internal/tarball`.

## Test speed

`rubyeval` (~25s) and `resolver` (~10s) shell out to Ruby and dominate a full
`go test ./...`. Run them when you change policyfile code; scope around them
otherwise.
