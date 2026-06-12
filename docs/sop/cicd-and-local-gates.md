# CI/CD and Local Gates SOP

**Project**: sumpter
**Governance**: Fulmen Ecosystem Standards
**Last Updated**: June 12, 2026

## Overview

Sumpter has two distinct classes of validation gate:

- **Remote CI/CD gates** run on GitHub Actions for every push and pull request.
  They must be hermetic: no live external services, no secrets, no developer
  machine state.
- **Local-only gates** run on a developer (or agent operator) machine before a
  push or PR. They may require things CI deliberately does not have — a live
  cloud endpoint, an ephemeral local server, a developer's own credentials.

This SOP records which targets belong to which class (with an explicit audit of
what remote CI invokes), and documents the **cloud live-integration test class**
that exploits the local-only gates to exercise real S3-compatible I/O.

## Table of Contents

1. [What runs on remote CI/CD](#what-runs-on-remote-cicd)
2. [What is local-only (never on remote CI)](#what-is-local-only-never-on-remote-ci)
3. [Cloud live-integration test class](#cloud-live-integration-test-class)
4. [Running the S3 live-integration suite](#running-the-s3-live-integration-suite)
5. [pr-final hardened gate](#pr-final-hardened-gate)
6. [Adding a new cloud provider (GCS, Azure, …)](#adding-a-new-cloud-provider-gcs-azure-)
7. [Gate summary](#gate-summary)

## What runs on remote CI/CD

The GitHub Actions workflows under `.github/workflows/` invoke only these Make
targets (audited 2026-06-12):

| Workflow                        | Make targets invoked                                                                               |
| ------------------------------- | -------------------------------------------------------------------------------------------------- |
| `ci.yml`                        | `make check-all`, `make test`, `make build`, `make release-build`, `make release-verify-checksums` |
| `release.yml`                   | `make lint`, `make test`, `make release-build`                                                     |
| `verify-embeds.yml`             | `make verify-embeds`                                                                               |
| `seekable-zstd-integration.yml` | CGO seekable-zstd tests (`-tags seekablezstd`)                                                     |

`make test` runs the **default** Go test build — that is, every test **except**
those behind a build tag. Build-tagged suites (`seekablezstd`, `s3integration`,
and future cloud tags) are excluded from `make test` and therefore from CI's
default run unless a workflow opts in explicitly (only `seekablezstd` does, in
its own dedicated workflow).

## What is local-only (never on remote CI)

The aggregate gate targets are **not referenced by any workflow** and run only on
a developer/operator machine:

- `make precommit` — fast pre-commit checks (the goneat pre-commit hook runs a
  subset automatically).
- `make prepush` — pre-push validation. Its checks _match_ what CI runs, but the
  **target itself is not invoked by CI**; CI calls the underlying `check-all` /
  `test` / `build` directly.
- `make pr-final` — the final local gate before opening/refreshing a PR.

> **Audit statement (2026-06-12):** `pr-final` and `prepush` are **never invoked
> by remote CI/CD**. CI runs the individual hermetic targets (`check-all`,
> `test`, `build`, …) directly. This is what makes it safe for `pr-final` to
> **require a live cloud endpoint** (see below): that requirement can never block
> or run on GitHub Actions.

If a workflow is ever added that calls `make pr-final` or `make prepush`, this
invariant breaks — update this SOP and reconsider the cloud-integration gate
before merging such a workflow.

## Cloud live-integration test class

Tests that need a **live S3-compatible endpoint** are a separate class, isolated
behind a Go build tag so they are invisible to the default/CI build:

- **Build tag:** `//go:build s3integration`. Files carrying it are excluded from
  `go test ./...` / `make test` (and thus from CI). They compile and run only
  under `-tags s3integration`.
- **Scope today:** the cloud read boundary — staged-to-local source acquisition,
  prefix listing, and the logical-URI-vs-staged-path no-leak contract, exercised
  end-to-end through the `extract` command (`cmd/sumpter/commands/extract_moto_test.go`)
  and the provider scaffold (`internal/uriio/moto_test.go`).
- **Hermetic counterpart stays in CI:** the in-core identity-threading test
  (`internal/extract`: `TestLogicalSourceIdentityNeverLeaksLocalPath`) is **not**
  tagged — it proves the staged-vs-logical split with a fake logical URI and a
  local temp file, so the no-leak guarantee is regression-protected on every CI
  run without any cloud dependency.

## Running the S3 live-integration suite

```bash
make test-integration-s3        # or: bash scripts/integration-s3.sh
```

The runner obtains an endpoint two ways, tried in order:

### 1. Bring-your-own (BYO) endpoint

Export the standard contract — a **URI plus an S3 credential reference** — and the
suite runs against it (moto, MinIO, AWS S3, or any S3-compatible provider). An
`https://` endpoint keeps TLS enforced; only a plaintext `http://` endpoint opts
into the insecure posture.

| Variable                   | Meaning                                                  | Required          |
| -------------------------- | -------------------------------------------------------- | ----------------- |
| `SUMPTER_TEST_S3_ENDPOINT` | Endpoint URI (e.g. `https://s3.us-east-1.amazonaws.com`) | yes               |
| `SUMPTER_TEST_S3_BUCKET`   | A pre-created bucket name                                | yes               |
| `SUMPTER_TEST_S3_PROFILE`  | AWS shared-config profile (preferred — see below)        | profile _or_ keys |
| `SUMPTER_TEST_S3_KEY_ID`   | Access key id (used only when no profile)                | profile _or_ keys |
| `SUMPTER_TEST_S3_SECRET`   | Secret access key (used only when no profile)            | profile _or_ keys |
| `SUMPTER_TEST_S3_REGION`   | Region (default `us-east-1`)                             | no                |

**Prefer a profile.** With `SUMPTER_TEST_S3_PROFILE`, credentials resolve from your
`~/.aws/credentials` via the AWS SDK — **no secret ever enters the environment, the
test process, or the on-disk credentials config the harness writes** (it records
only `profile: <name>`). This also exercises Sumpter's profile-handle credential
path against a real provider. Use literal `KEY_ID`/`SECRET` only where a profile is
not available.

These vars are deliberately namespaced (`SUMPTER_TEST_S3_*`) so the suite never
picks up a developer's ambient `AWS_*` credentials by accident. This is enforced
**fail-closed**: when an endpoint and bucket are set, the runner (and the test
harnesses) require exactly one explicit credential mode — a profile, or both
literal keys — and abort before any request rather than letting the AWS default
credential chain (env / shared config / IMDS) silently take over. Half-pairs and
profile+literal mixing are rejected.

#### Local setup with direnv (`.envrc`)

`.envrc` is gitignored, so it is the natural home for your local, machine-specific
endpoint + profile + bucket; credentials themselves resolve from
`~/.aws/credentials` via the profile. A profile-based `.envrc` looks like:

```bash
# .envrc (gitignored) — your local, machine-specific test config
export SUMPTER_TEST_S3_ENDPOINT="https://<your-s3-compatible-endpoint>"
export SUMPTER_TEST_S3_REGION="<region>"
export SUMPTER_TEST_S3_PROFILE="<your-aws-profile>"   # resolves creds from ~/.aws
export SUMPTER_TEST_S3_BUCKET="<your-test-bucket>"
```

These values are per-developer, so they live in `.envrc` and `~/.aws/*` rather
than in the repository.

### 2. Self-provisioned ephemeral moto

With no BYO endpoint set, the runner stands up a throwaway [moto](https://github.com/getmoto/moto)
server in a cached Python virtualenv (`.cache/integration/motovenv`, gitignored),
creates a bucket, runs the suite, and tears the server down. Requirements:

- `python3` on `PATH`.
- Network access on the **first** run only (to `pip install moto[server]`); the
  venv is cached and reused afterward.

### Escape hatch

```bash
SUMPTER_SKIP_S3_INTEGRATION=1 make test-integration-s3   # loud skip, exit 0
```

For a genuinely offline machine with no BYO endpoint. This is the documented way
to pass `make pr-final` without the harness. Use it sparingly — skipping forfeits
the cloud-path coverage for that run.

## pr-final hardened gate

`make pr-final` **requires** the S3 live-integration suite:

```
pr-final: prepush examples confidentiality-tree-check test-integration-s3 pr-final-drift-check
```

Because `pr-final` is local-only (audited above), this requirement never reaches
CI. On a normal developer machine the suite self-provisions moto, so the gate is
satisfied automatically; `SUMPTER_SKIP_S3_INTEGRATION=1` is the explicit, loud
opt-out for offline cases. `test-integration-s3` runs **before**
`pr-final-drift-check`, and leaves no tracked-file drift (its artifacts live under
gitignored `.cache/` and `/tmp`).

## Adding a new cloud provider (GCS, Azure, …)

The class is provider-scoped by design so additional backends slot in without
disturbing S3. To add, say, GCS live tests:

1. Tag the live tests `//go:build gcsintegration` (keep any hermetic counterpart
   untagged so CI still regression-protects the core logic).
2. Add `scripts/integration-gcs.sh` mirroring `integration-s3.sh`: BYO endpoint
   first (`SUMPTER_TEST_GCS_*`), then a self-provisioned emulator
   (e.g. `fake-gcs-server`), with a `SUMPTER_SKIP_GCS_INTEGRATION` escape hatch.
3. Add `make test-integration-gcs` and append it to the `pr-final` prerequisites.
4. Extend this SOP's [gate summary](#gate-summary) and the audit table.

Keeping one provider per tag/target/script means a missing or flaky emulator for
one backend never blocks the others.

## Gate summary

| Gate                         | Where it runs                 | Cloud endpoint? | Notes                                          |
| ---------------------------- | ----------------------------- | --------------- | ---------------------------------------------- |
| `check-all`, `test`, `build` | Remote CI + local             | No (hermetic)   | Default Go build; build-tagged suites excluded |
| `seekablezstd` tests         | Dedicated CI workflow + local | No              | CGO; own build tag                             |
| `precommit` / `prepush`      | Local only                    | No              | Mirror CI checks; not invoked by CI            |
| `test-integration-s3`        | Local / on-demand             | **Yes** (S3)    | `s3integration` tag; BYO or ephemeral moto     |
| `pr-final`                   | Local only                    | **Yes** (S3)    | Requires the S3 suite; never on CI             |
