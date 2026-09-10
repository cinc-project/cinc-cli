---
name: acceptance-tests
description: Use when writing, running, or debugging acceptance tests under test/acceptance, covering the cinc-zero harness, the seeded chef-repo fixtures, and pinning a new cinc-zero release
---

# Acceptance tests

Acceptance tests live under `test/acceptance/` and run the real compiled binary
against a live [`cinc-zero`](https://github.com/cinc-project/cinc-server-ng)
server, a single-binary in-memory Chef Infra Server. They are gated behind the
`acceptance` build tag.

```
go test -tags acceptance ./test/...
# or
make test-acceptance
```

The harness downloads and caches the pinned cinc-zero release automatically, so
no Ruby is needed. Set `CINC_ZERO_BIN` to point at a local build instead.

## Seed data

cinc-zero preloads the `test/acceptance/seed/` chef-repo into the `acme` org via
`--repo`: nodes, roles, environments, clients, data bags, a policy, and a policy
group.

The global users and the `devs` group, which the chef-repo format cannot
express, are seeded separately by the harness through the cinc CLI itself.

Tests share that seed, but each test runs against its own fresh cinc-zero
instance, so a test may mutate server state freely.

## Pinning a new cinc-zero release

Bump `cincZeroVersion` in `test/acceptance/helpers_test.go`.

## Coverage manifest

Every shipped leaf command must appear in `test/acceptance/coverage_manifest.toml`,
either `status = "covered"` with the acceptance test function name(s) or
`status = "exempt"` with a reason. `coverage_meta_test.go` walks the live cobra
tree and fails CI on a missing entry, a reasonless exemption, or a `covered`
entry naming a test that does not exist.

## Socket paths

`t.TempDir()` embeds the test name and can blow past the 104 byte `sun_path`
limit on macOS, failing with a bare `bind: invalid argument`. Use a short
`os.MkdirTemp("", "...")` path for anything binding a unix socket.
