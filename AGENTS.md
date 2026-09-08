# AGENTS.md

Guidance for Codex when working in this repository.

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

- **All server communication goes through `github.com/cinc-project/cinc-api`.** That
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
- `make test` / `go test ./...` — run the test suite.
- `make vet`, `make fmt` — `go vet` and `gofmt`.
- `make docs` — regenerate the per-command Markdown reference under
  `docs/commands/` from the live cobra command tree.
- `make help` — list all targets.

Acceptance tests live under `test/acceptance/` and run the real binary
against a live `chef-zero` server. They are gated behind the
`acceptance` build tag and need Ruby plus the `chef-zero` gem:

```
go test -tags acceptance ./test/...
# or
make test-acceptance
```

The chef-zero helper at `test/acceptance/chef-zero-server.rb` seeds a
fixed set of nodes, roles, environments, clients, and data bags into
the `acme` org; tests share that seed but each test runs against its
own fresh chef-zero instance.

## Conventions

- **Test-driven development.** Write a failing test first, watch it fail for the
  expected reason, then write the minimal code to pass.
- **Every command needs both unit and acceptance tests.** Adding or
  modifying a `cinc <noun> <verb>` command is not done until:
  1. A unit test in `apps/cinc/cmd/<noun>_test.go` drives the cobra
     command end-to-end against an `httptest` server (fast, deterministic,
     no external dependencies).
  2. An acceptance test in `test/acceptance/<noun>_test.go` runs the real
     compiled binary against `chef-zero` and asserts on the same
     behavior. If the chef-zero response shape or seed makes a code path
     untestable in acceptance, document the gap inline and cover it in
     the unit test instead.
  3. `go test ./...` and `go test -tags acceptance ./test/...` both pass.
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
   configured `cinc-api` client and the chosen output format.
4. Render results through `cli/printer` — never format output inline.
5. Add unit tests in `apps/cinc/cmd/<noun>_test.go` and acceptance
   tests in `test/acceptance/<noun>_test.go`. Both are required (see
   Conventions).
6. Run `make docs` so the per-command reference under `docs/commands/`
   picks up the new command, short/long help, and flags. CI also runs
   this on every push to `main` and commits the result, but landing
   the docs alongside the code keeps PR review honest.
