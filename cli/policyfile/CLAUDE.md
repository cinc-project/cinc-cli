# cli/policyfile

## Gotchas

- **`CookbookLock.Origin()` picks by key precedence, not by intent.** `cinc-api`
  checks `source_options` in the order path, artifactserver, git, chef_server,
  and returns the first hit (the call site is `fetch.go`). A lock carrying both
  a repository URL and a `path` is therefore classified as a *path* source, and
  the repository fetch never runs. Reason about which branch actually executes
  before concluding a code path is reachable.
- **Two places decide what counts as a cookbook file.** `copyTree` here (for
  export bundles) and `archiveEntries` in `cli/cookbook` (for uploads) walk a
  cookbook independently. Changing the rules in one without the other makes
  `upload` and `export` disagree about the same directory.

## Test seams

`maxExtractedFileBytes` and `maxExtractedArchiveBytes` in `extract.go` are the
extraction caps. Tests shrink them rather than building giant fixtures; restore
them with `t.Cleanup` or a deferred restore.

## Test speed

`rubyeval` (~25s) and `resolver` (~10s) shell out to Ruby and dominate a full
`go test ./...`. Run them when you change policyfile code; scope around them
otherwise.
