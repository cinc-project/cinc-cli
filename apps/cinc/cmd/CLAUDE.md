# apps/cinc/cmd

The Cobra command tree: one file per noun group (`node.go`, `role.go`, …), plus
`root.go` (root command, persistent flags) and `common.go` (flag-resolution
helpers).

Keep this layer thin. Business logic belongs in the `cli/*` packages, which is
where most tests live.

Adding a `cinc <noun> <verb>` command has a fixed procedure: use the
`adding-a-command` skill.

## Gotchas

- **`bufio.Reader` returns `("", io.EOF)` at end of input but `("\n", nil)` for
  a bare Enter.** Prompt loops that reject an empty answer must tell those apart
  or they spin forever when stdin is closed. `promptWithDefault` (`create.go`)
  treats EOF as "accept the default"; a prompt with no default cannot.
- **Styled output helpers already exist.** For indented steps, ✓/✗ and colored
  tags, reuse `useColor` / `colorize` / `mark` and the `ansi*` constants in
  `config_checks.go` — they already handle `NO_COLOR` and TTY detection, so
  output stays plain when piped or under tests. Render structured data through
  `cli/printer`, never inline.

## Test seams are package-level vars

The codebase avoids interfaces-for-testing in favour of swappable package vars,
each documented at its declaration. Reach for the existing one rather than
restructuring, and restore it with `t.Cleanup`:

| Seam | Declared in |
|------|-------------|
| `stdinIsTTY`, `migrateChef`, `runFirstRunConfigure` | `common.go` |
| `resolveHost` | `config_checks.go` |
| the editor hooks | `editor.go` |
| `nodeRemoteRunner` | `node.go` |
| `tlsWarnWriter`, `SilenceTLSWarning` | `cli/client` |

## Isolate the environment, or you will test the developer's machine

`resolveConfigPath` falls back to the real `~/.cinc/credentials` whenever
`--config` is unset, and several helpers read `$HOME`. A test that forgets
either will quietly pass or fail based on whoever's laptop it runs on.

- Drive the real command tree with `--config <tempfile>` in `SetArgs`.
- For unit-testing a helper that takes a `*cobra.Command`, use `fakeCmd` from
  `common_test.go`. Setting a persistent flag on a root command **before**
  `Execute()` does not reach `cmd.Flags()`, so
  `root.PersistentFlags().Set("config", ...)` followed by a direct helper call
  silently reads the real credentials file instead.
- `t.Setenv("HOME", t.TempDir())` whenever the code under test might look there.
- Watch for a command falling back to a public default (the Chef Supermarket,
  omnitruck) when config resolution misses. The test still passes, it is just
  slow, flaky, and talking to the internet. A suspiciously long test is the
  usual tell.
