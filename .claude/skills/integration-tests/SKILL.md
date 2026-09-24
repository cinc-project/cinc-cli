---
name: integration-tests
description: Use when writing, running, or debugging integration suite cases under integration/, covering how a case is declared, the cinc-server-ng and cinc-server-erlang targets, gaps, the coverage guard, and the helpers for terminals, credentials and assertions
---

# Integration tests

The integration suite runs the real `cinc` binary against real server
implementations. It is its own Go module under `integration/`, so the
cinc-server-ng dependency never reaches the root module.
`integration/README.md` is the reference for the targets, the AWS stack and
the case-writing rules; this skill is the working checklist.

```sh
make test-integration                                      # from the repo root
cd integration && go test ./cincserverng/ -run 'TestCincServerNG/cli/'   # one family
```

Set `CINC_BIN` to test an already-built binary instead of building one.

## Every command needs a case

Every leaf command needs a unit test (`apps/cinc/cmd/`) and at least one
suite case that lists it in `covers`. `TestCoverage`
(`suite/coverage_test.go`) walks the live cobra tree and fails CI for a leaf
command that is neither covered nor in `exempt` (`suite/coverage.go`) with a
reason. It runs without a server, in every `go test ./...` of the module.

## Writing a case

A case is `{"<family>/<case>", covers, func(t, Target, *cli)}` in its
family's list. Write it once against `suite.Target`; it then runs against
every target. Each case:

- runs in parallel with every other case on one shared server, so it names
  what it creates with `uniqueName` and registers `c.cleanup(...)`;
- depends on no seed data and no other case;
- asserts on `--format json` where the command offers it;
- wraps reads that go through search in `eventually`, since erchef indexes
  asynchronously.

Assertion traps that have caused flaky failures:

- Match an HTTP status with `hasStatus(s, 403)`, never
  `strings.Contains(s, "403")`: every `uniqueName` ends in random hex, so a
  bare substring also matches inside object names.
- An error is one line: `behStderrLine` fails the case on a stack of wrapped
  fragments or a usage dump.
- A binary written during the run (`behRenamedCLI`) can be briefly
  unexecutable on Linux while a parallel case forks; `waitUntilExecutable`
  waits that out.

## Helpers for setup and credentials cases

- `newCLI` gives each case a throwaway HOME with `[default]` and `[other]`
  profiles signing as the target's admin; `behBareCLI` gives an empty HOME
  for first-run and chef-compat cases.
- `execTTY(answers, args...)` runs on a pseudo-terminal, so the first-run
  prompts fire. Answers are queued before the process starts, one line per
  prompt; `lines(...)` builds them and `eot` is Ctrl-D. Queue a trailing
  answer in cases that expect no prompt, so a regression fails instead of
  hanging for 60s.
- `behChefCredentials`, `chefProfile` and `migrateOnFirstRun` set up and
  migrate a knife `~/.chef/credentials`; `configureAnswers` answers the
  configure prompts; `validate()` decodes `config validate --format json`.

## Gaps

When a server gets a behavior wrong, list the case in that target's `Gaps`
with the reason, rather than weakening the assertion. On cinc-server-ng
every gap links an upstream issue in cinc-project/cinc-server-ng and comes
out when a release fixes it: after bumping the version in
`integration/go.mod`, run the gapped cases with their skip removed to find
the ones that now pass. `suite.Run` rejects a gap that names no case or has
no reason.

## cinc-server-erlang

The real CINC Server runs by hand in AWS through
`integration/run-cinc-server-erlang.sh` (about 10 minutes and $0.17/hour).
Any change to a request the CLI sends should be run there before it
merges: cinc-server-ng accepting a request does not mean erchef will.
