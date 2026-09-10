# CLAUDE.md

Guidance for coding agents working in this repository.
`AGENTS.md` is a symlink to this file, so Codex and Claude Code read the
same instructions and cannot drift apart.

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

## Architecture rules

- **All server communication goes through `github.com/cinc-project/cinc-api`.**
  That library owns authentication (request signing), transport, and the API
  object model. The CLI never builds or signs an HTTP request. The single seam
  between CLI state and the API library is `cli/client`.
- Commands are **noun-verb**: `cinc <noun> <verb>` (e.g. `cinc node list`). The
  core verbs `list`/`show`/`create`/`edit`/`delete` mean the same thing on every
  noun.
- Keep the command layer thin. `apps/cinc/cmd/` is one file per noun group;
  business logic belongs in the `cli/*` packages, which is where most tests live.
- **Validate every flag, then act.** Resolve flags before anything with a side
  effect. Rejecting a bad `--format` is worthless once the command has already
  created a client on the server or SSHed into a host.

## Build & test

- `make build` — compile the binary; version metadata is injected via `-ldflags`.
  The binary lands at `./cinc` in the repo root (not `./bin/`).
- `make test` / `go test ./...` — run the test suite.
- `make vet`, `make fmt` — `go vet` and `gofmt`. Both must be clean before you
  commit.
- `make docs` — regenerate the per-command Markdown reference under
  `docs/commands/` from the live cobra tree. Needed whenever a command, its
  help strings, or its flags change.
- `make test-acceptance` — the real binary against a live `cinc-zero` server,
  gated behind the `acceptance` build tag. See the `acceptance-tests` skill.

While iterating, scope `go test` to the packages you touched (e.g.
`go test ./cli/supermarket/ ./apps/cinc/cmd/`). A full `go test ./...`
is dominated by `cli/policyfile/rubyeval` (~25s) and
`cli/policyfile/resolver` (~10s), which shell out to Ruby — only run
those when you're changing policyfile code.

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
- **Every command needs both a unit and an acceptance test**, plus an entry in
  `test/acceptance/coverage_manifest.toml`. A meta-test walks the live cobra
  tree and fails CI if a shipped leaf command is missing from the manifest.
  Use the `adding-a-command` skill.
- **Tests must not touch the network.** An httptest server is the only
  acceptable endpoint.
- **Test seams are swappable package-level vars**, each documented at its
  declaration. Reach for the existing one rather than restructuring, and
  restore it with `t.Cleanup`.

## Where the details live

This file stays small on purpose. Everything below loads on demand, either when
a skill is invoked or when you open a file in that directory:

| Topic | Where |
|-------|-------|
| Adding a `cinc <noun> <verb>` command | `adding-a-command` skill |
| cinc-zero harness, seed data, version pinning | `acceptance-tests` skill |
| Command tree, test seams, environment isolation | `apps/cinc/cmd/CLAUDE.md` |
| Cookbook file rules, extraction caps | `cli/cookbook/CLAUDE.md` |
| Lock origins, export bundles, Ruby test speed | `cli/policyfile/CLAUDE.md` |
| Credentials file merge rules | `cli/config/CLAUDE.md` |
| Concurrency and unix socket tests | `cli/remote/CLAUDE.md` |
| Every config key and its chef-compat pair | `docs/configuration.md` |
| Command surface and flags | `docs/commands/` |
| Design documents | `docs/dev/` |
