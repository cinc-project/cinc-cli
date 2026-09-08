# cinc

`cinc` is a single, unified command-line tool for Cinc Infra (with full
Chef Infra compatibility): one command with one consistent grammar. It
is a Go binary built with
[Cobra](https://github.com/spf13/cobra) on top of the
[`cinc-api`](https://github.com/cinc-project/cinc-api) client library, which owns
authentication and transport.

## Why Cinc CLI

The existing Chef toolchain works, but it carries a lot of historical
weight. `cinc` is a fresh take on the same job, with a few things we
weren't willing to compromise on:

- **Performance.** A native Go binary with no interpreter to spin up,
  so configuring a system or scripting against the server feels
  instant instead of sluggish.
- **Ease of installation.** A single static binary with no runtime
  dependencies. Nothing to break when the system Ruby is upgraded,
  no gem environment to babysit, and no multi-step bootstrap in your
  CI pipelines. Drop the binary in and go.
- **Ease of setup.** `cinc config create` walks you through credentials,
  server URL, and SSL settings interactively, so a new user (or an
  existing Chef user pointing at a new server) is productive in
  minutes instead of losing an afternoon to troubleshooting.
- **Focused experience.** Scoped to day-to-day node and cookbook
  management against a Cinc/Chef server. No migration helpers, no
  one-off subcommands for legacy workflows, just the verbs you
  reach for every day, kept consistent across every noun.
- **Well documented.** Every command, subcommand, and flag has a
  generated Markdown reference under [`docs/commands/`](docs/commands/),
  rebuilt from the live cobra command tree on every change, so the
  docs can't drift from what the binary actually does.
- **Fully tested.** Every command ships with both unit tests against
  an in-process HTTP server and acceptance tests that run the real
  compiled binary against a live `cinc-zero`, so behavior is verified
  end-to-end before it lands.

## Status

Beta. `cinc` has feature parity with the everyday Chef workflows it
targets, and the major ideas (the noun-verb grammar, the consistent
core verbs, the config and compatibility model) are settled. What we
need now is real-world use: point `cinc` at your server, run your
actual workflows, and [open an issue](https://github.com/cinc-project/cinc-cli/issues)
when something doesn't behave the way you expect.

Expect the command surface to keep shifting as we act on that feedback
(flags, output, and the occasional verb may change before 1.0), but
the foundations are stable enough to build on. See
[`docs/commands/cinc.md`](docs/commands/cinc.md) for the current command
surface (auto-generated from the cobra command tree); the taxonomy and
internal architecture are described in [`docs/dev/`](docs/dev/).

## Install

Requires Go 1.26 or newer.

```sh
# Build a local ./cinc binary
make build

# Or install into $GOBIN
make install
```

`make` injects version, commit, and build-date metadata into the binary
via `-ldflags`; `cinc version` will print them.

### Install in GitHub Actions

Tagged releases publish prebuilt Linux and macOS archives. A workflow can install
the Linux AMD64 binary without installing Go:

```yaml
- name: Install cinc
  env:
    CINC_VERSION: v0.21.0
  run: |
    archive="cinc_${CINC_VERSION}_linux_amd64.tar.gz"
    curl -fsSL -o "$archive" "https://github.com/cinc-project/cinc-cli/releases/download/${CINC_VERSION}/${archive}"
    tar -xzf "$archive"
    sudo install "cinc_${CINC_VERSION}_linux_amd64/cinc" /usr/local/bin/cinc
    cinc version
```

Release archives are built by `make dist` and include `SHA256SUMS`.

## Configure

`cinc` reads a TOML credentials file (default `~/.cinc/credentials`)
holding named profiles, in the same shape as Chef's `~/.chef/credentials`.
Each top-level section is a profile that points at one Cinc/Chef Server.
The quickest way to create one is `cinc config create`, which walks you
through it interactively.

```toml
[default]
cinc_server_url = "https://cinc.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"
```

Persistent flags on every command:

| Flag | Description |
| --- | --- |
| `--config` | Path to the credentials file (default `~/.cinc/credentials`) |
| `--profile` | Profile name to use (default: `$CINC_PROFILE`, then `$CHEF_PROFILE`, then `default`) |
| `--format` | Output format: `human` or `json` |

Every chef-prefixed key and environment variable has a cinc-prefixed
equivalent (the `cinc_`/`CINC_` form wins when both are set), so an
existing Chef setup works unchanged.

- **[`docs/configuration.md`](docs/configuration.md)**: the complete
  reference covering every config key, multi-profile setups, profile
  selection, encrypted data bag secrets, Supermarket upload identities,
  and the `cinc config create` / `cinc config validate` commands.
- **[`docs/migrating-from-chef.md`](docs/migrating-from-chef.md)**: for
  knife and Chef Workstation users, covering how to reuse your existing
  `~/.chef/credentials`, the chef/cinc key duality, and how knife
  commands map onto cinc's noun-verb grammar.

## Usage

Commands are noun-verb. The core verbs (`list`, `show`, `create`,
`edit`, `delete`) mean the same thing on every noun. For a guided
walkthrough (profiles, output formats, common workflows), see
[`docs/README.md`](docs/README.md). For the exhaustive per-command
reference (every flag, every default), see
[`docs/commands/`](docs/commands/). The reference pages are
regenerated from the live cobra command tree by `make docs` and
refreshed automatically on every push to `main`.

## Develop

```sh
make test            # unit tests
make vet             # go vet
make fmt             # gofmt -w
make test-acceptance # acceptance tests against cinc-zero (auto-downloads the cinc-zero binary)
make help            # list all targets
```

Repository layout:

- `apps/cinc/`: the binary. `cinc.go` is a thin `main()`; `cmd/` holds
  the Cobra command tree (one file per noun group, plus `root.go` and
  flag-resolution helpers in `common.go`).
- `cli/config`: TOML config parsing and profile resolution.
- `cli/client`: builds a `cinc-api` client from a resolved profile. This
  is the single seam between CLI state and the API library; the CLI
  itself never builds or signs an HTTP request.
- `cli/printer`: renders command output as human text or JSON.
- `docs/`: auto-generated per-command reference under `commands/`
  (regenerated by `make docs`) and design docs under `dev/`.
- `tools/gendocs/`: small Go program that walks the cobra command
  tree and writes the Markdown reference. Invoked by `make docs`.
- `test/`: acceptance tests, gated behind the `acceptance` build tag.

See [`CLAUDE.md`](CLAUDE.md) for conventions followed when developing
with Claude Code.

## License

Licensed under the [Apache License 2.0](LICENSE).
