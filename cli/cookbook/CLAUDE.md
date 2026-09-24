# cli/cookbook

## Gotchas

- **cinc-api decides what a cookbook is, in one place.** Which files belong
  to a cookbook (Chef's loader rules: chefignore, root dot-directories,
  chef-zero's sentinel, symlinks) and what its metadata says come from
  `cinc.LocalCookbookFromDir`, its `Files()` and `Identifiers()`, and
  `cinc.LoadCookbookMetadata`. `cinc cookbook upload`, `cinc supermarket
  share`, `cinc policy export`/`push` and the resolver's identifiers all use
  them, so they agree about the same directory. Don't walk a cookbook
  directory or parse metadata.rb here; if the rules are wrong, fix cinc-api.
- **Load cookbooks through `Load`.** It passes an absolute path, so a cookbook
  whose metadata sets no name is named after its directory rather than `.`
  when the user is standing in it, and it turns a computed metadata.rb
  version into a message the user can act on.

## Test seams

Tarball extraction lives in `cli/internal/tarball`, which owns the extraction
caps (`maxFileBytes`, `maxArchiveBytes`). Its tests shrink them; callers'
tests don't need to.
