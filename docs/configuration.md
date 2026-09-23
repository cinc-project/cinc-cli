# Configuring cinc

This is the complete reference for how `cinc` finds and reads your
credentials. If you're coming from knife or Chef Workstation, start with
[Migrating from Chef](migrating-from-chef.md) for the quick path, then
come back here when you want the full picture.

## The credentials file

`cinc` keeps its connection settings in a single
[TOML](https://toml.io) file. By default that's `~/.cinc/credentials`,
the same shape and location convention as Chef's `~/.chef/credentials`.
Point at a different file per command with the global `--config` flag:

```sh
cinc node list --config /path/to/credentials
```

A leading `~` in `--config` is expanded to your home directory, so
`--config=~/.chef/credentials` works even though no shell expands a `~`
after `=`.

The file holds one or more **profiles**. A profile is a named bundle of
everything needed to talk to one server (or one Supermarket): a server
URL, a client identity, a signing key, and a handful of optional
settings. Each top-level TOML table is a profile, and the table name is
the profile name:

```toml
[default]
cinc_server_url = "https://cinc.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"

[staging]
cinc_server_url = "https://staging.example.com/organizations/acme-staging"
client_name     = "tim"
client_key      = "/keys/staging.pem"
ssl_verify_mode = ":verify_none"
```

You can keep as many profiles as you like in one file (a default, a
staging server, a Supermarket-only identity) and choose between them
per command. See [Selecting a profile](#selecting-a-profile) below.

## Every configuration key

All keys are strings. Only `client_name`, `client_key`, and a server
endpoint are needed for a normal server profile; everything else is
optional.

Every key that holds a path (`client_key`, `trusted_certs_dir`,
`secret_file`, `supermarket_key`) may start with `~`, which is expanded
to your home directory when `cinc` reads it. When `cinc` rewrites the
file it keeps the `~` as you wrote it.

| Key | Meaning | Required? | Default | Chef-compat equivalent | Related flag / env |
| --- | --- | --- | --- | --- | --- |
| `cinc_server_url` | Server URL, including the `/organizations/<org>` segment | Yes (or a Supermarket site) | — | `chef_server_url` | `--server-url` / `--cinc-server-url` |
| `client_name` | Client/user name requests are signed as | Yes | — | (same key) | `--client-name` |
| `client_key` | Path to the RSA private key (PEM) used to sign requests | Yes | — | (same key) | `--client-key` |
| `ssl_verify_mode` | TLS verification: `:verify_peer` or `:verify_none` | No | `:verify_peer` | (same key) | `--ssl-verify-mode` |
| `trusted_certs_dir` | Directory of extra CA certificates (`*.crt`, `*.pem`) to trust | No | `~/.cinc/trusted_certs`, then `~/.chef/trusted_certs`, when present | (same key) | — |
| `secret_file` | Path to the default encrypted data bag secret | No | — | (same key) | `--secret-file`, `$CINC_SECRET_FILE` / `$CHEF_SECRET_FILE` |
| `supermarket_site` | Supermarket instance the `cinc supermarket` commands target | No | `https://supermarket.chef.io` | (same key) | `--supermarket-site` |
| `supermarket_client_name` | Username used to sign Supermarket uploads | No | falls back to `client_name` | none (cinc-only) | — |
| `supermarket_key` | Path to the key used to sign Supermarket uploads | No | falls back to `client_key` | none (cinc-only) | — |

> The chef-prefixed `chef_server_url` is accepted everywhere
> `cinc_server_url` is. When both appear in the same profile, the
> cinc-prefixed value wins. When `cinc` writes a profile (via `cinc
> config create` or first-run migration), it emits the cinc-canonical
> `cinc_server_url`. A profile that already carries `chef_server_url`
> keeps it too, updated to the same value, because knife reads only that
> key: dropping it would leave knife on its built-in default server URL.
> A profile that never had it stays cinc-canonical only. See
> [Migrating from Chef](migrating-from-chef.md#chef--and-cinc-prefixed-keys)
> for the full duality story.

### `cinc_server_url` and the organization split

The server URL must include the `/organizations/<org>` segment, exactly
as knife expects it:

```toml
cinc_server_url = "https://cinc.example.com/organizations/acme"
```

`cinc` splits that into a bare server URL (`https://cinc.example.com`)
and an organization (`acme`) internally; you never configure the two
separately. A URL without the `/organizations/<org>` segment is
reported as an error by `cinc config validate` (see
[Validating a profile](#cinc-config-validate)).

A profile that only talks to Supermarket, never to a Cinc Server, can
omit the server URL entirely and set `supermarket_site` instead.

### `client_name` and `client_key`

`client_name` is the identity the server knows you by; `client_key` is
the path to that identity's RSA private key in PEM form. `cinc` reads
the key to sign every request; it's never uploaded. A leading `~` in
the path is expanded to your home directory. If the key file is missing
or unreadable, `cinc` tells you which path it tried and which profile
pointed there.

### `ssl_verify_mode`

Controls TLS certificate verification when talking to the server. Two
values are accepted:

- `:verify_peer` (the default): verify the server's certificate, as
  you'd want in production.
- `:verify_none`: skip verification. Handy against a lab server with a
  self-signed certificate, but don't ship it.

Any other value is rejected by `cinc config validate`. The leading
colon matches knife's Ruby-symbol spelling.

### `trusted_certs_dir`

Points `cinc` at a directory of extra CA certificates to trust when it
talks to the server. Use it when your server's certificate comes from an
internal or self-signed CA: instead of turning verification off with
`ssl_verify_mode = ":verify_none"`, drop the CA certificate into the
directory and keep full verification.

```toml
[default]
client_name       = "tim"
client_key        = "/keys/tim.pem"
cinc_server_url   = "https://cinc.internal.example.com/organizations/acme"
trusted_certs_dir = "~/.cinc/trusted_certs"
```

Every `*.crt` and `*.pem` file in the directory is read, and the
certificates it holds are trusted in addition to your system's
certificates (the system store is never replaced). Other files and
subdirectories are ignored. A leading `~` is expanded to your home
directory; any other relative path is relative to the directory you run
`cinc` from.

When the key is unset, `cinc` looks for `~/.cinc/trusted_certs` and uses
it if it exists. For chef compatibility it falls back to
`~/.chef/trusted_certs`, which is where `knife ssl fetch` saves
certificates, so an existing knife setup works as is. If neither
directory exists, only the system certificates are trusted.

A `trusted_certs_dir` you set explicitly must exist: if it doesn't,
every server command stops and tells you so. A certificate file that
holds nothing `cinc` can parse is skipped without complaint on normal
commands; `cinc config validate` lists those files so you can clean them
up.

The key name matches knife's `trusted_certs_dir`, so the same
credentials file serves both tools. It only affects connections to the
Cinc Server, not the Supermarket commands.

### `secret_file` and encrypted data bags

`secret_file` points at the shared key used to encrypt and decrypt
**encrypted data bag items** (`cinc databag secret …`). It's the
per-profile default; an individual command can always override it. When
a `cinc databag secret` command needs the secret, it resolves one from
the first source that's set, in this order:

1. `--secret <literal>`: the flag value is the secret, used verbatim.
2. `--secret-file <path>`: the file's full contents are the secret.
3. `$CINC_SECRET_FILE`, then `$CHEF_SECRET_FILE` (cinc wins).
4. the profile's `secret_file` key.

`--secret` and `--secret-file` can't be combined. A secret file is read
the way Chef reads it: leading and trailing whitespace (such as the
newline most editors add) is stripped, and whatever is inside is kept,
so an existing `encrypted_data_bag_secret` works unchanged and items
stay readable by knife and chef-client. A file that's empty once
stripped is refused. A `--secret` literal is used exactly as given. The
on-disk key name matches knife's `knife[:secret_file]`.

### `supermarket_site` and the upload identity

`supermarket_site` sets which Supermarket instance the `cinc
supermarket` commands talk to. It defaults to the public
`https://supermarket.chef.io`; set it to point at a private Supermarket.

By default, `cinc supermarket share` signs uploads with the profile's
own `client_name` and `client_key`, the same identity it uses against
your Cinc Server. The public Supermarket usually wants a *different*
identity (your Supermarket account, not your Chef client), so two
optional keys let you override it:

- `supermarket_client_name`: the Supermarket username uploads are
  signed as.
- `supermarket_key`: path to the private key used to sign uploads.

Each falls back **independently**: the effective username is
`supermarket_client_name` or, when unset, `client_name`; the effective
key is `supermarket_key` or, when unset, `client_key`. So you can
override just the username, just the key, or both. When neither is set,
uploads use `client_name`/`client_key`. These are **cinc-only** keys:
knife has no equivalent, so there's no chef-prefixed pairing.

## Selecting a profile

When a command needs a profile and you don't name one, `cinc` picks one
in this order, stopping at the first that's set:

1. the `--profile <name>` flag
2. the `$CINC_PROFILE` environment variable
3. the `$CHEF_PROFILE` environment variable
4. the profile literally named `default`

```sh
cinc node list                      # uses [default]
cinc node list --profile staging    # uses [staging]
CINC_PROFILE=staging cinc role list # sticky for the shell
```

When both `$CINC_PROFILE` and `$CHEF_PROFILE` are set, `CINC_PROFILE`
wins, the same cinc-over-chef rule that applies to the config keys.

> The `cinc supermarket` commands resolve a profile slightly
> differently: with no `--profile` or profile env var, they prefer a
> profile literally named `supermarket`, falling back to `default`.
> This lets you keep your public-Supermarket identity in its own
> `[supermarket]` profile without it shadowing your server profile.

## Environment variables

| Variable | Effect | Cinc wins over |
| --- | --- | --- |
| `CINC_PROFILE` | Selects the active profile | `CHEF_PROFILE` |
| `CHEF_PROFILE` | Selects the active profile (chef-compat) | — |
| `CINC_SECRET_FILE` | Default encrypted data bag secret path | `CHEF_SECRET_FILE` |
| `CHEF_SECRET_FILE` | Default encrypted data bag secret path (chef-compat) | — |
| `NO_COLOR` | Disables bold/colored terminal styling | — |
| `CINC_RUBY_WASM_DIR` | Directory holding an already-extracted Policyfile runtime (see below) | — |
| `CINC_RUBY_WASM_URL` | Mirror to download the pinned Policyfile runtime from | — |

### The Policyfile evaluation runtime

`cinc policy install` evaluates your `Policyfile.rb` with CRuby compiled
to WebAssembly. That runtime is not built into the binary: the first
time it is needed, cinc downloads the pinned `ruby.wasm` release from
GitHub, verifies its SHA-256, and caches the extracted tree under your
OS cache directory (for example `~/.cache/cinc-cli/ruby-wasm/<version>`
on Linux). Every later run reuses the cache, re-checking the module's
checksum each time.

That first download is around 25 MB, so two overrides exist for machines
that cannot reach GitHub:

- `CINC_RUBY_WASM_URL` points the download at a mirror. The checksum is
  still enforced, so the mirror has to serve the exact pinned release.
- `CINC_RUBY_WASM_DIR` points at a directory that already holds the
  extracted release. cinc reads from it and never writes to it. Because
  setting it is an explicit instruction, a directory that does not hold
  the pinned release is reported as an error rather than quietly
  replaced by the download you were trying to avoid.

Both the directory here and the packaged one below hold the release as
the archive extracts it, so the path you name is the one **containing**
the top-level release directory, not the release directory itself:

```
/srv/ruby-wasm/                                  <- name this path
  ruby-3.4-wasm32-unknown-wasip1-full/
    usr/local/bin/ruby
```

Packagers can bake the same thing in at build time so an installed cinc
never downloads anything, by extracting the pinned release into a
directory inside the package and naming it with
`-ldflags "-X github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval.packagedRuntimeDir=/path/to/dir"`.
That one is a default rather than an instruction, so if it is missing or
holds a different release cinc falls back to the cache and the download
instead of failing. This means a binary upgraded ahead of its runtime
payload keeps working. `CINC_RUBY_WASM_DIR` is tried first when both are
set.

Whichever source the module comes from, its compiled form is cached per
user under the OS cache directory after the first run. That is a
compilation cache, not a copy of the release.

## Global flags

These persistent flags work on every command:

| Flag | Description |
| --- | --- |
| `--config` | Path to the credentials file (default `~/.cinc/credentials`) |
| `--profile` | Profile to use (default: `$CINC_PROFILE`, then `$CHEF_PROFILE`, then `default`) |
| `--format` | Output format: `human` (default) or `json` |

## Creating and editing profiles

### `cinc config create`

`cinc config create` writes a profile into your credentials file. With
no flags it runs an interactive walkthrough, prompting for each value
and offering a sensible default you can accept with Enter:

```sh
cinc config create
```

If the credentials file already has profiles, it asks whether to add a
new one, update an existing one, or replace the file. Pass `--config`
to write somewhere other than `~/.cinc/credentials`, and `--profile` to
name the profile.

Supply any of the setup flags and it runs **non-interactively** instead,
which is what you want in scripts:

```sh
cinc config create \
  --profile staging \
  --cinc-server-url https://staging.example.com/organizations/acme-staging \
  --client-name tim \
  --client-key ~/.cinc/staging.pem \
  --ssl-verify-mode :verify_none
```

The available flags are:

| Flag | Sets |
| --- | --- |
| `--server-url` / `--cinc-server-url` / `--chef-server-url` | the server URL (all three are aliases) |
| `--supermarket-site` | `supermarket_site` |
| `--client-name` | `client_name` |
| `--client-key` | `client_key` |
| `--ssl-verify-mode` | `ssl_verify_mode` |

In non-interactive mode `--client-name` and `--client-key` are
required. `config create` writes TOML only; `cinc` never emits Ruby
`config.rb`/`client.rb` files.

> `config create` does **not** have flags for `secret_file`,
> `trusted_certs_dir`, `supermarket_client_name`, or `supermarket_key`. Add those by editing
> the credentials file directly; it's plain TOML. Note also that
> rewriting a profile through `config create` does not preserve comments
> or the original key ordering in the file.

### `cinc config validate`

`cinc config validate` runs local pre-flight checks against your
credentials, and pings each server to confirm it's reachable. It checks
that:

- the file is valid TOML,
- every profile has a `client_name` and a `client_key`,
- each profile has a usable endpoint (`cinc_server_url`,
  `chef_server_url`, or `supermarket_site`),
- any server URL includes the `/organizations/<org>` segment,
- `ssl_verify_mode`, when set, is `:verify_peer` or `:verify_none`,
- the trusted certificates directory, when set or found at a default
  location, exists and every `*.crt`/`*.pem` file in it holds a
  certificate (unparseable files are a warning, not a failure),
- `supermarket_site`, when set, is a valid URL,
- and each configured server actually answers (reporting its TLS
  posture as its own check).

```sh
cinc config validate            # checks ~/.cinc/credentials
cinc config validate ./creds    # checks a specific file
cinc config validate --format json
```

It's local-and-reachability only; it never modifies your config. If a
check fails, `cinc config create` will let you fix the profile.

## Worked examples

### A basic single-profile setup

```toml
[default]
cinc_server_url = "https://cinc.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"
```

### A default plus a lab/staging profile

The staging server uses a self-signed certificate, so it disables TLS
verification:

```toml
[default]
cinc_server_url = "https://cinc.example.com/organizations/acme"
client_name     = "tim"
client_key      = "/keys/tim.pem"
secret_file     = "/keys/encrypted_data_bag_secret"

[staging]
cinc_server_url = "https://staging.example.com/organizations/acme-staging"
client_name     = "tim"
client_key      = "/keys/staging.pem"
ssl_verify_mode = ":verify_none"
```

```sh
cinc node list                     # production
cinc node list --profile staging   # the lab server
```

### Talking to a server and publishing to the public Supermarket

This profile uses your normal Chef client against your Cinc Server, but
overrides the identity for `cinc supermarket share` so cookbooks publish
under your public Supermarket account. It also sets a default encrypted
data bag secret:

```toml
[default]
cinc_server_url         = "https://cinc.example.com/organizations/acme"
client_name             = "tim"
client_key              = "/keys/tim.pem"
secret_file             = "/keys/encrypted_data_bag_secret"
supermarket_client_name = "tim-public"
supermarket_key         = "/keys/supermarket.pem"
```

```sh
cinc node list                       # signed as "tim" against your server
cinc supermarket share my_cookbook   # signed as "tim-public" to Supermarket
```
