---
name: adding-a-command
description: Use when adding or modifying a `cinc <noun> <verb>` command, covering the constructor, registration, flag resolution, the required unit test and integration suite case, the coverage guard, and regenerating docs
---

# Adding a server command

A command is not done until every step here is green. Steps 5 to 7 are the ones
that fail CI if skipped.

## 1. Write the command

1. Add (or extend) `apps/cinc/cmd/<noun>.go` with a `new<Noun>Cmd()` constructor.
2. Register it in `root.go` via `root.AddCommand(...)`.
3. Use `resolveClient(cmd)` and `resolveFormat(cmd)` from `common.go` to obtain a
   configured `cinc-api` client and the chosen output format.

   **Resolve flags before anything with a side effect.** `resolveFormat` rejects
   an unknown `--format`, and that rejection is worthless if it happens after the
   command has already created a client on the server or SSHed into a host. The
   rule is: validate every flag, then act. (This is not hypothetical: `node
   bootstrap` once generated a key, created the client, SSHed in and ran the
   installer before rejecting `--format yaml`, leaving an orphaned client and a
   half-configured host.)
4. Render results through `cli/printer`, never format output inline.

Commands are noun-verb, and the core verbs `list` / `show` / `create` / `edit` /
`delete` must mean the same thing on every noun. All server communication goes
through `github.com/cinc-project/cinc-api`; the CLI never builds or signs an HTTP
request.

## 2. Test it (both kinds are required)

5. **Unit test** in `apps/cinc/cmd/<noun>_test.go`, driving the cobra command
   end-to-end against an `httptest` server: fast, deterministic, no external
   dependencies. See `apps/cinc/cmd/CLAUDE.md` for the environment-isolation
   rules, which you will get wrong otherwise.
6. **Integration suite case** in `integration/suite/<family>.go`, running the
   real compiled binary against a live server and asserting on the same
   behavior. List the case in its family with the leaf commands it covers
   (`covers`). Write it once against `suite.Target`; it runs against the
   in-process cinc-server-ng in CI and against real erchef by hand. See
   `integration/README.md` for the rules a case follows. If a server gets
   the behavior wrong, list the case in that target's `Gaps` with a link to
   the upstream issue rather than weakening the assertion.

Both `go test ./...` and `make test-integration` must pass.

## 3. Record and document it

7. The suite's coverage guard (`integration/suite/coverage_test.go`) walks
   the live cobra tree and **fails CI** if a shipped leaf command is neither
   covered by a case nor listed in `exempt` (`integration/suite/coverage.go`)
   with a reason, such as needing the external Supermarket service. This is
   what lets us say everything we ship is tested against a real server.
8. Run `make docs` to regenerate the per-command reference under `docs/commands/`.
   CI also runs this on every push to `main` and commits the result, but landing
   the docs alongside the code keeps PR review honest.

   Changing an existing `Short` / `Long` / `Example` or a flag's help string
   needs `make docs` too, not just adding a command.

## Help strings

No em dashes in `Short` / `Long` / `Example`: they generate `docs/commands/`.
Use a comma, colon, parentheses, or two sentences. Keep the tone conversational,
the way the rest of the CLI talks to users.
