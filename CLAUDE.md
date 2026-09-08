# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project

`cinc` is a single, unified command-line tool for Cinc Infra (with full Chef
Infra compatibility) — one command with one consistent grammar. It is a Go
binary built with [Cobra](https://github.com/spf13/cobra).

## Project identity: cinc-first, chef-compatible

This tool is for **cinc**. Treat chef as a compatibility target, not the focus:

- **User-facing docs and examples** describe cinc flows first. Cinc-prefixed
  config keys, env vars, and file paths are the canonical form; the chef
  equivalents are mentioned as a compatibility note, not lead-with material.
- **Internal naming uses cinc.** Functions, types, packages, fixtures, and
  test names should say `cinc`, or be neutral, unless the symbol's job is
  specifically to handle chef-compat behavior — in which case the chef name
  is correct and clearer (e.g. `TestLoadAcceptsChefServerURLKey`).
- **Strive for backwards compatibility with existing Chef tools** (knife,
  chef workstation, chef-zero). Existing users should be able to point cinc
  at their existing `~/.chef/credentials`, env vars, and key files and have
  it work. **But every chef-prefixed config option or env var MUST also have
  a cinc-prefixed equivalent**, and when both are present the cinc form
  wins. The compatibility tables in README and tests are the source of
  truth for which knobs need a paired form.

Design documents live in `docs/dev/`:

- `docs/dev/*-cinc-cli-command-taxonomy.md` — the user-facing command structure
- `docs/dev/*-cinc-cli-internal-architecture.md` — how the CLI is built

User-facing usage docs live in `docs/`. The current command surface and
all flags are documented in `docs/commands.md`.

## Git workflow

- **All work happens in a git worktree, never on `main`.** Before starting any
  task, create an isolated worktree off the latest `origin/main` (use the
  `using-git-worktrees` skill, or `git worktree add`). Never edit, commit, or
  push from a plain `main` checkout.
- **Never commit or push directly to `main`.** Every change lands through a
  pull request opened from the worktree's branch. A bare `git push` that
  reports `main -> main` means something has gone wrong — stop and move the
  work to a branch.
- **Branch from the very latest `origin/main`.** Run `git fetch origin` first;
  work lands fast here, and a stale base produces redundant or un-rebaseable
  PRs. Confirm with `git branch --show-current` before every commit and push.

## Repository layout

- `apps/cinc/` — the binary.
  - `cinc.go` — thin `main()`; calls `cmd.Execute()`.
  - `cmd/` — the Cobra command tree. One file per noun group (`node.go`, …),
    plus `root.go` (root command, persistent flags) and `common.go`
    (flag-resolution helpers).
- `cli/` — reusable, independently-testable infrastructure:
  - `cli/config` — parses the TOML config file and resolves named profiles.
  - `cli/client` — builds a `cinc-api` client from a resolved profile.
  - `cli/printer` — renders command output as human text or JSON.
- `docs/` — design documents.

## Architecture rules

- **All server communication goes through `github.com/tas50/cinc-api`.** That
  library owns authentication (request signing), transport, and the API object
  model. The CLI never builds or signs an HTTP request. The single seam between
  CLI state and the API library is `cli/client`.
- Commands are **noun-verb**: `cinc <noun> <verb>` (e.g. `cinc node list`). The
  core verbs `list`/`show`/`create`/`edit`/`delete` mean the same thing on every
  noun.
- Keep the command layer thin — business logic belongs in the `cli/*` packages,
  which is where most tests live.

## Build & test

- `make build` — compile the binary; version metadata is injected via `-ldflags`.
  The binary lands at `./cinc` in the repo root (not `./bin/`).
- `make test` / `go test ./...` — run the test suite.
- `make vet`, `make fmt` — `go vet` and `gofmt`.
- `make docs` — regenerate the per-command Markdown reference under
  `docs/commands/` from the live cobra command tree.
- `make help` — list all targets.

While iterating, scope `go test` to the packages you touched (e.g.
`go test ./cli/supermarket/ ./apps/cinc/cmd/`). A full `go test ./...`
is dominated by `cli/policyfile/rubyeval` (~25s) and
`cli/policyfile/resolver` (~10s), which shell out to Ruby — only run
those when you're changing policyfile code.

For styled human output (indented steps, ✓/✗, colored tags), reuse the
`useColor`/`colorize`/`mark` helpers and `ansi*` constants in
`apps/cinc/cmd/config_checks.go` — they already handle `NO_COLOR` and
TTY detection, so output stays plain when piped or under tests. Render
structured data through `cli/printer`.

Acceptance tests live under `test/acceptance/` and run the real binary
against a live [`cinc-zero`](https://github.com/cinc-project/cinc-server-ng) server
— a single-binary, in-memory Chef Infra Server. They are gated behind
the `acceptance` build tag. The harness downloads and caches the pinned
cinc-zero release automatically (no Ruby needed); set `CINC_ZERO_BIN`
to point at a local build instead.

```
go test -tags acceptance ./test/...
# or
make test-acceptance
```

cinc-zero preloads the `test/acceptance/seed/` chef-repo (nodes, roles,
environments, clients, data bags, a policy, and a policy group) into
the `acme` org via `--repo`. The global users and the `devs` group,
which the chef-repo format can't express, are seeded separately by the
harness through the cinc CLI. Tests share that seed but each runs
against its own fresh cinc-zero instance. When pinning a new cinc-zero
release, bump `cincZeroVersion` in `test/acceptance/helpers_test.go`.

## Conventions

- **Conversational tone in user-facing strings.** Prompts, success messages,
  and error messages talk to the user like a teammate, not a compiler. Prefer
  contractions ("we found", "you're"), full sentences, and concrete next
  steps ("run `cinc config create` to set one up") over terse, lowercased
  fragments ("no credentials"). Lead with what happened from the user's
  point of view; reserve technical detail for when it changes what they
  should do next.
- **No em dashes in user-facing docs.** Don't use the em dash character
  (`—`, U+2014) in user-facing documentation (`README.md`, anything under
  `docs/`) or in cobra command help strings (`Short`/`Long`/`Example`),
  since those generate the reference under `docs/commands/`. Use a comma,
  colon, parentheses, or two sentences instead. The em-dash separators the
  doc generator (`tools/gendocs`) emits count too: keep them out of its
  output. This is both a house style and a way to keep generated docs from
  reading as machine-written. One exception: a lone em dash as a placeholder
  in a table cell, meaning "none" or "not applicable", is fine, since that's
  a conventional table notation rather than prose.
- **Test-driven development.** Write a failing test first, watch it fail for the
  expected reason, then write the minimal code to pass.
- **Every command needs both unit and acceptance tests.** Adding or
  modifying a `cinc <noun> <verb>` command is not done until:
  1. A unit test in `apps/cinc/cmd/<noun>_test.go` drives the cobra
     command end-to-end against an `httptest` server (fast, deterministic,
     no external dependencies).
  2. An acceptance test in `test/acceptance/<noun>_test.go` runs the real
     compiled binary against `cinc-zero` and asserts on the same
     behavior. If the cinc-zero response shape or seed makes a code path
     untestable in acceptance, document the gap inline and cover it in
     the unit test instead.
  3. `go test ./...` and `go test -tags acceptance ./test/...` both pass.
  4. The command is recorded in `test/acceptance/coverage_manifest.toml` —
     either `status = "covered"` with the acceptance test function name(s),
     or `status = "exempt"` with a reason (e.g. it needs the external
     Supermarket service or an interactive TTY). The `coverage_meta_test.go`
     meta-test walks the live cobra tree and **fails CI** if a shipped leaf
     command is missing from the manifest, an exemption has no reason, or a
     `covered` entry names a test that doesn't exist. A command isn't done
     until its manifest entry is green — this is what lets us say everything
     we ship is tested against a real server.
- Run `gofmt` and `go vet ./...` before committing; both must be clean.
- Configuration is a TOML file (`~/.cinc/credentials` by default) holding
  named profiles. Each top-level section is a profile carrying
  `cinc_server_url` (or, for chef compatibility, `chef_server_url`),
  `client_name`, `client_key`, and an optional `ssl_verify_mode`. The CLI
  splits the `/organizations/<org>` segment off the server URL internally.
  Profile selection: `--profile` flag → `$CINC_PROFILE` → `$CHEF_PROFILE` →
  `default`. The on-disk shape mirrors Chef's `~/.chef/credentials` so
  existing knife users can point cinc at their existing file unchanged.

## Adding a server command

1. Add (or extend) `apps/cinc/cmd/<noun>.go` with a `new<Noun>Cmd()` constructor.
2. Register it in `root.go` via `root.AddCommand(...)`.
3. Use `resolveClient(cmd)` and `resolveFormat(cmd)` from `common.go` to obtain a
   configured `cinc-api` client and the chosen output format. **Resolve flags
   before anything with a side effect.** `resolveFormat` rejects an unknown
   `--format`, and that rejection is worthless if it happens after the command
   has already created a client on the server or SSHed into a host. The rule
   is: validate every flag, then act.
4. Render results through `cli/printer` — never format output inline.
5. Add unit tests in `apps/cinc/cmd/<noun>_test.go` and acceptance
   tests in `test/acceptance/<noun>_test.go`. Both are required (see
   Conventions).
6. Add the new leaf command(s) to `test/acceptance/coverage_manifest.toml`
   (`status = "covered"` with the acceptance test name(s), or `status =
   "exempt"` with a reason). The acceptance `coverage_meta_test.go` fails CI
   if a shipped command is missing from the manifest.
7. Run `make docs` so the per-command reference under `docs/commands/`
   picks up the new command, short/long help, and flags. CI also runs
   this on every push to `main` and commits the result, but landing
   the docs alongside the code keeps PR review honest. Changing an existing
   `Short`/`Long`/`Example` or a flag's help string also needs `make docs`,
   not just adding a command.

## Writing tests here

### Isolate the environment, or you will test the developer's machine

`resolveConfigPath` falls back to the real `~/.cinc/credentials` whenever
`--config` is unset, and several helpers read `$HOME`. A test that forgets
either will quietly pass or fail based on whoever's laptop it runs on.

- Drive the real command tree with `--config <tempfile>` in `SetArgs`.
- For unit-testing a helper that takes a `*cobra.Command`, use `fakeCmd`
  from `common_test.go`. Setting a persistent flag on a root command
  **before** `Execute()` does not reach `cmd.Flags()`, so
  `root.PersistentFlags().Set("config", ...)` followed by a direct helper
  call silently reads the real credentials file instead.
- `t.Setenv("HOME", t.TempDir())` whenever the code under test might look
  there.

### Test seams are package-level vars

The codebase avoids interfaces-for-testing in favour of swappable package
vars, each documented at its declaration. Reach for the existing one rather
than restructuring: `stdinIsTTY`, `migrateChef`, `runFirstRunConfigure`
(`common.go`), `resolveHost` (`config_checks.go`), the editor hooks
(`editor.go`), `tlsWarnWriter` plus `SilenceTLSWarning` (`cli/client`),
`nodeRemoteRunner` (`node.go`), and the extraction caps in
`cli/policyfile` and `cli/cookbook`. Restore them with `t.Cleanup`.

### Tests must not touch the network

An httptest server is the only acceptable endpoint. Watch for the case where
a command falls back to a public default (the Chef Supermarket, omnitruck)
when config resolution misses: the test still passes, it is just slow and
flaky, and it is talking to the internet. A suspiciously long test is the
usual tell.

### Concurrency

Anything touching `cli/remote` should be run with `-race -count=N`. Beware
stub runners that return instantly: they finish before the next job is even
dispatched, so a test meaning to exercise concurrent work may be testing
nothing. Use a barrier that blocks until every worker has genuinely started.

### Unix sockets in tests

`t.TempDir()` embeds the test name and blows past the 104 byte `sun_path`
limit on macOS, failing with a bare `bind: invalid argument`. Use a short
`os.MkdirTemp("", "...")` path for socket tests.

## Gotchas worth knowing

- **`CookbookLock.Origin()` picks by key precedence, not by intent.**
  `cinc-api` checks `source_options` in the order path, artifactserver, git,
  chef_server, and returns the first hit. A lock carrying both a repository
  URL and a `path` is therefore classified as a *path* source, and the
  repository fetch never runs. Reason about which branch actually executes
  before concluding a code path is reachable.
- **Two places decide what counts as a cookbook file.** `archiveEntries`
  in `cli/cookbook` (for uploads) and `copyTree` in `cli/policyfile` (for
  export bundles) walk a cookbook independently. Changing the rules in one
  without the other makes `upload` and `export` disagree about the same
  directory.
- **The credentials file is shared with knife.** It holds keys this CLI has
  no model for. Anything that rewrites it must merge, not re-serialize a
  struct, or those keys are silently dropped.
- **`bufio.Reader` returns `("", io.EOF)` at end of input but `("\n", nil)`
  for a bare Enter.** Prompt loops that reject an empty answer must tell
  those apart or they spin forever when stdin is closed. `promptWithDefault`
  treats EOF as "accept the default"; a prompt with no default cannot.
