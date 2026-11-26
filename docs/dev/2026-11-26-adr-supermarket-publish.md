# ADR: Add `cinc supermarket publish`

- **Date** 2026-11-26
- **Status** Accepted for implementation
- **Intended implementer** Tim (`tas50`)

## Context

`knife supermarket share` and the existing `cinc supermarket share` package a
local cookbook and submit a legacy upload signed with a Chef account's PEM key.
CINC Supermarket's legacy facade serves cookbook reads. Its native API already
supports publishing into Supermarket-managed OCI storage through a publish intent,
an OCI upload and digest-based finalisation. Today that workflow takes several
manual steps. [CINC Supermarket publishing walkthrough][publish]

## Decision

Add `cinc supermarket publish <namespace>/<cookbook>` as the one-command
replacement for `knife supermarket share` when publishing to CINC Supermarket.
The command publishes straight to the chosen CINC Supermarket installation. Its
OCI proxy sends the bytes to Supermarket-managed storage. Publishers need no GHCR
account, Chef account key or PEM file. Keep `share` available for existing Chef
Supermarket installations. [Publishing and legacy compatibility decision][legacy]

Reuse local cookbook packaging, including the existing file selection and
`metadata.json` generation. Derive the version from cookbook metadata and check
that its name matches the scoped target. Extend `cinc-supermarket-api` for native
Supermarket API calls and use an OCI client for artifact transfer. Keep the Cobra
command focused on arguments, credential selection and output.

### Human and CI credentials

- A human uses a CINC Supermarket bearer token obtained through the GitHub
  device-flow exchange. Support secure token input for an existing credential.
- A trusted GitHub Actions or GitLab CI workload exchanges a provider-issued OIDC
  token at `POST /api/v2/login/ci` for a short-lived Supermarket bearer. CI runs
  without prompts or a stored user credential. The grant belongs to the registered
  workflow and its allowed scope. [Trusted publishing][ci]
- Both paths open a publish intent, upload to the OCI proxy with intent-bound
  credentials and finalise using the exact OCI manifest digest. Keep credentials
  out of arguments, logs and output. Report the scoped name, version and manifest
  digest on success. Use the existing `--format json` convention for scripts.
- The namespace owner designates an owning team before a new cookbook's first
  publication. If the server rejects the intent because the team is missing, the
  command tells the publisher to ask the namespace owner to assign a team, then
  retry. This applies to people and CI, before upload. The designated team owns
  the cookbook and Supermarket records the publishing actor separately. Later
  releases preserve ownership and existing release bytes stay immutable.

### Signed SBOMs

Support uploading the cookbook OCI artifact together with an optional,
publisher-signed SPDX SBOM attestation. CI and human publishers can supply signed
evidence for the artifact being uploaded. A signed statement identifies the exact
cookbook **OCI manifest digest** as its subject and covers the SPDX bytes, either
by including them or by binding their digest. Attach the evidence as OCI referrers
of that cookbook digest. A tag, a tarball digest alone or a signature over a
standalone SBOM cannot establish that binding. Use standard OCI and Sigstore
formats where supported. [OCI artifact subjects][oci] · [Sigstore attestations][sigstore]

Publisher verification is optional. An ordinary publish still works without
publisher-signed evidence. If the publisher supplies evidence, the command must
surface a subject mismatch or failed attachment and must never report that the
requested signed publication succeeded in that case. Registry authentication and
publisher signing are separate operations. Use externally supplied evidence or a
keyless signing flow without requiring a publisher PEM key. [Publisher verification decision][trust]

Supermarket's own acceptance signature independently records the cookbook and
registry SPDX digests it accepted. The publisher attestation establishes which
publisher vouched for the inventory for that digest when verification succeeds.
The registry signature supports checking that the accepted bytes and inventory
have stayed intact. A publish token carries upload authority only. [Registry acceptance signatures][acceptance]

## Server dependency and acceptance

The current publish pipeline generates its own SPDX inventory. It does not yet
ingest and expose publisher-signed evidence on this path. Coordinate the
attestation format, authorised referrer transfer and finalisation contract with
CINC Supermarket before releasing signed support. The server must associate the
submitted evidence with the finalised digest, verify the subject and SBOM binding,
and report publisher verification independently from registry acceptance. Agree
the exact Sigstore predicate and signer trust policy with the server implementers.
[Signing work item][signing]

Verify the CLI with unit tests and a real-binary integration test against a local
test server. Qualify it against a CINC Supermarket instance with its OCI proxy and
Zot. Cover human token and trusted CI release, first-publish rejection before
upload and retry after team designation, exact digest finalisation, and a
publish without publisher evidence. The signed test must independently verify an
uploaded attestation for the cookbook digest and fail on changed SBOM bytes or a
different subject. Test that a failed attachment cannot result in a reported
signed publish, that a retry cannot overwrite a published version, and that CI
emits no prompts or secrets.

[publish]: https://gitlab.com/cinc-project/distribution/cinc-supermarket/-/blob/78ac4789/docs/DEVELOPMENT.md#7-the-phase-2-write-surfaces-auth-publish-lifecycle
[legacy]: https://gitlab.com/cinc-project/distribution/cinc-supermarket/-/blob/78ac4789/docs/adr/0037-public-publishing-and-alias-administration.md
[ci]: https://gitlab.com/cinc-project/distribution/cinc-supermarket/-/blob/78ac4789/docs/adr/0023-ci-trusted-publishing.md
[oci]: https://specs.opencontainers.org/image-spec/manifest/
[sigstore]: https://docs.sigstore.dev/cosign/signing/other_types/#sboms-software-bill-of-materials
[trust]: https://gitlab.com/cinc-project/distribution/cinc-supermarket/-/work_items/16#note_4335637580
[acceptance]: https://gitlab.com/cinc-project/distribution/cinc-supermarket/-/blob/78ac4789/docs/adr/0030-registry-acceptance-signatures.md
[signing]: https://gitlab.com/cinc-project/distribution/cinc-supermarket/-/work_items/16#note_4335653397
