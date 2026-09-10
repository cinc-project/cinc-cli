# cli/cookbook

## Gotchas

- **Two places decide what counts as a cookbook file.** `archiveEntries` here
  (for uploads) and `copyTree` in `cli/policyfile` (for export bundles) walk a
  cookbook independently. Changing the rules in one without the other makes
  `cinc cookbook upload` and `cinc policyfile export` disagree about the same
  directory. Change both, and test both.

## Test seams

`maxExtractedFileBytes` and `maxExtractedArchiveBytes` in `extract.go` are the
extraction caps. Tests shrink them rather than building giant fixtures; restore
them with `t.Cleanup` or a deferred restore.
