# Integration tests

End-to-end tests of the `cinc` binary against real server implementations.
This directory is its own Go module, so the cinc-server-ng test dependency
never reaches the root module. The binary under test is still built from the
root module, exactly as a release build is.

| Package | What it is | When it runs |
|---|---|---|
| `suite/` | Every case, written once against a `suite.Target`, plus the coverage guard | Coverage guard on every `go test ./...` here |
| `cincserverng/` | Runs the suite against an in-process [cinc-server-ng](https://github.com/cinc-project/cinc-server-ng) with signature checks and ACL enforcement on | Every pull request, every push to `main`, and every release tag before its binaries are published |

```sh
make test-integration            # from the repository root
cd integration && go test ./...  # the same thing
go test ./cincserverng/ -run 'TestCincServerNG/nodes/'   # one family
```

Set `CINC_BIN` to run the suite against an already-built binary (a release
archive, say) instead of building one.

## Writing a case

Each family (nodes, roles, cookbooks, ...) lives in its own file in `suite/`
and lists its cases with the leaf commands each one covers. A case gets a
`cli` that runs the binary in a throwaway `HOME` whose credentials sign as the
target's admin, and:

- names every object it creates with `uniqueName`, and registers a
  `c.cleanup(...)` delete for it, so cases run in parallel and reruns against
  a long-lived server never collide;
- depends on no seed data and no other case;
- asserts on `--format json` output where the command offers it;
- wraps anything that reads through search in `eventually`, since erchef
  indexes asynchronously.

`TestCoverage` walks the live command tree and fails if a leaf command is
neither covered by a case nor listed, with a reason, in `exempt`
(`suite/coverage.go`). Families still being ported from `test/acceptance`
list their commands in `pending`.

## Gaps

A case one server cannot pass is listed in that target's `Gaps` with the
reason. On cinc-server-ng every gap links an upstream issue and is removed
once the issue is fixed. `suite.Run` rejects a gap that names no case or has
no reason.
