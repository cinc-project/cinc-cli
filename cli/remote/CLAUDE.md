# cli/remote

## Testing

- **Run with `-race -count=N`.** Anything in this package is concurrent by
  definition, and a single pass proves very little.
- **Beware stub runners that return instantly.** They finish before the next job
  is even dispatched, so a test meaning to exercise concurrent work may be
  testing nothing. Use a barrier that blocks until every worker has genuinely
  started.
- **Unix sockets need short paths.** `t.TempDir()` embeds the test name and
  blows past the 104 byte `sun_path` limit on macOS, failing with a bare
  `bind: invalid argument`. Use `os.MkdirTemp("", "...")` for socket tests.
