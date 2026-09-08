# API improvements

This directory tracks improvements we'd like in the
[`github.com/cinc-project/cinc-api`](https://github.com/cinc-project/cinc-api) library
(and, where it reaches that far, the Cinc/Chef Server API behind it) that
would let the CLI do its job more simply, cheaply, or correctly.

These are **not** CLI design docs; those live one level up in
[`docs/dev/`](../). An entry here describes a rough edge the CLI works
around today and the API change that would remove the workaround. Each
one is a candidate to upstream into `cinc-api`.

## How to add one

One file per improvement, named `NNNN-short-slug.md` (zero-padded,
monotonically increasing). Copy the shape of an existing entry:

- **Summary**: one or two sentences: what's awkward and what we want.
- **Where it bites**: the CLI code that pays the cost today, with
  `file:line` references so the entry stays anchored to real code.
- **Why it matters**: the concrete cost (extra round trips, missing
  permissions, wrong results, …).
- **Proposed improvement(s)**: ranked options, ideally with the API
  shape we'd want.
- **Workaround until then**: what the CLI can do on its own in the
  meantime, if anything.

Keep entries honest about status: an idea we haven't validated against the
server's real capabilities should say so.

## Index

| # | Improvement | Status |
|---|-------------|--------|
| [0001](0001-user-admin-status-without-double-call.md) | Resolve a user's admin status without a second `groups/admins` call | Proposed |
| [0002](0002-group-description.md) | Carry a human-readable description on groups | Proposed |
