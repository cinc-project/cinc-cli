# `cinc` CLI: Internal Architecture Design

- **Date:** 2026-05-19
- **Status:** Draft (for review)
- **Scope:** Internal architecture and code organization of the `cinc` CLI: how
  the tool is built. The user-facing command taxonomy (verbs, nouns, grouping)
  is covered separately in `2026-05-19-cinc-cli-command-taxonomy.md`.

## Goals

- A single Go binary, `cinc`.
- A strict separation between server communication and CLI behavior.
- A command layer that mirrors the noun-verb taxonomy with minimal repetition.
- Reusable infrastructure broken into small, independently-testable packages.

## The `cinc-api` Boundary

The most important architectural rule: **all Cinc/Chef Server communication goes
through the `github.com/cinc-project/cinc-api` library.**

`cinc-api` owns:

- Request signing and authentication (the server's API auth protocol).
- HTTP transport, TLS configuration, retries, and error decoding.
- The API object model: nodes, roles, environments, cookbooks, data bags,
  clients, search, and the associated CRUD operations.

The CLI owns everything else, and never:

- Constructs or signs raw HTTP requests.
- Knows the wire format of the server API.

The seam between the two is a single `cli/client` package, which converts
resolved CLI configuration and credentials into a configured `cinc-api` client.
Command code depends on `cinc-api` types for data and on `cli/client` to obtain a
client. Nothing else in the CLI imports transport or auth concerns.

This keeps the server API surface swappable and independently versioned, and
keeps command code focused purely on user experience.

## Technology Choices

- **Language:** Go.
- **CLI framework:** `spf13/cobra` for the command tree, `spf13/viper` for
  layered configuration.
- **Logging:** `rs/zerolog`.
- **Interactive UI:** a `charmbracelet/bubbletea`-based set of components for
  prompts, selectors, and editor integration.

## Repository Layout

```
cinc-cli/
├── apps/
│   └── cinc/
│       ├── cinc.go            # main(): thin entrypoint, calls cmd.Execute()
│       └── cmd/
│           ├── root.go        # root command, persistent flags, logger init
│           ├── crud.go        # generic list/show/create/edit/delete builder
│           ├── node.go        # `cinc node` and its verbs
│           ├── cookbook.go
│           ├── role.go
│           ├── environment.go
│           ├── databag.go
│           ├── client.go
│           ├── user.go
│           ├── group.go
│           ├── policy.go
│           ├── policygroup.go
│           ├── repo.go
│           ├── supermarket.go
│           ├── config.go
│           ├── server.go
│           ├── search.go      # global utility verb
│           ├── shellinit.go   # global utility verb
│           └── version.go     # global utility verb
├── cli/
│   ├── config/                # config file, profiles, credentials, paths
│   ├── client/                # the cinc-api seam
│   ├── printer/               # output rendering (human, JSON)
│   ├── errors/                # CommandError type with exit codes
│   ├── theme/                 # terminal color and styling
│   └── components/            # interactive prompts, selectors, editor
├── docs/
├── go.mod                     # module github.com/cinc-project/cinc-cli
└── Makefile
```

There are two top-level source trees:

- **`apps/cinc/`**: the binary and its command definitions. `cinc.go` is a thin
  `main()` that calls `cmd.Execute()`; the `cmd` package holds the entire cobra
  command tree.
- **`cli/`**: reusable infrastructure, each package with one clear purpose,
  importable and testable on its own.

## Command Layer (`apps/cinc/cmd`)

The taxonomy is noun-verb, and the command tree mirrors it directly:

- `root.go` builds the root `cinc` command, registers persistent flags
  (`--server`, `--profile`, `--format`, `--verbose`), and initializes logging in
  cobra's `PersistentPreRun`.
- **One file per noun group** (`node.go`, `cookbook.go`, …). Each file constructs
  the noun's `*cobra.Command` and attaches its verb sub-commands.
- `search.go`, `shellinit.go`, and `version.go` provide the global utility verbs.

### Generic CRUD builder

The taxonomy guarantees that `list`, `show`, `create`, `edit`, and `delete` mean
the same thing on every noun. `crud.go` exposes a builder that, given a resource
binding to `cinc-api`, produces those five verb commands with identical behavior.

Each noun file calls the builder for its core verbs and hand-writes only its
resource-specific verbs: `cinc cookbook upload`, `cinc node bootstrap`,
`cinc policy push`, and so on. This removes boilerplate and guarantees the five
core verbs behave identically across all nouns.

## CLI Infrastructure (`cli/`)

- **`cli/config`**: loads and writes the config/credentials file, resolves the
  active profile and server, computes config paths, and backs the `cinc config`
  commands.
- **`cli/client`**: the `cinc-api` seam that turns resolved config and credentials
  into a configured client. The only package that wires CLI state to the API
  library.
- **`cli/printer`**: renders command output. One renderer per format
  (human-readable text/tables and JSON) selected by `--format`. Commands hand the
  printer typed `cinc-api` objects and never format output inline.
- **`cli/errors`**: a `CommandError` type carrying a message and a process exit
  code, so command failures map to meaningful, documented exit statuses.
- **`cli/theme`**: terminal color and styling, OS-aware, with a no-color
  fallback.
- **`cli/components`**: interactive components, including confirmation prompts,
  selectors, and the `$EDITOR` integration used by `edit` verbs.

Each package depends only on the standard library, `cinc-api` types, and a small
set of well-supported third-party libraries, and is unit-tested in isolation.

## Configuration & Credentials

- A single TOML config file holds named **profiles**; each profile carries a
  server URL, organization, client/user name, and key path.
- `viper` layers configuration in precedence order: built-in defaults < config
  file < environment variables < command-line flags.
- The `cinc config` commands (`list`, `show`, `use`, `edit`, `path`) operate on
  this file through `cli/config`.
- Credentials (signing keys) are referenced by path. The CLI passes them to
  `cinc-api`, which performs the actual request signing. The CLI never touches
  the signing algorithm.

## Output & Rendering

- Every command runs a typed result through `cli/printer`.
- `--format human` (the default) and `--format json` are supported.
- Because rendering is isolated, adding a new output format later touches only
  `cli/printer`.

## Error Handling & Exit Codes

- Commands return errors; `cmd.Execute()` inspects them at the top level.
- `cli/errors.CommandError` distinguishes user-facing failures and assigns
  process exit codes (success, generic failure, and a distinct code for
  configuration errors) so scripts and CI can branch on the outcome.
- Errors surfaced by `cinc-api` are wrapped with CLI context, never leaked raw.

## Cross-Cutting Concerns

- **Logging** uses `zerolog`, initialized once in the root command's
  `PersistentPreRun`; `--verbose` raises the level.
- The command layer is kept deliberately thin so that most logic lives (and is
  tested) in the `cli/*` packages.

## Out of Scope / Open Questions

- The final Go module path (`github.com/cinc-project/cinc-cli` is assumed).
- A **plugin model** for third-party commands (cloud provisioning and similar) is
  deferred; the cobra tree can accept externally-registered commands once
  designed.
- **Self-update** and CI-environment detection: useful later, not required for
  a first release.
- An **interactive shell mode**: not planned for the first release.
