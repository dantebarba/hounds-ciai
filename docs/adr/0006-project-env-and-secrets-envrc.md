# 0006. Project env/secrets via `.envrc`, unwrapped host-side (v1)

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)

## Context

The doer needs the *project's* env and secrets (test DB URLs, private-registry
tokens, `NODE_ENV`, and complex blobs like service-account JSON) — distinct from
the agent's own OAuth creds ([0002](0002-docker-per-doer-sandbox-via-herdr-agent.md)).
A committed file must never hold secret *values*, and complex/multiline secrets
are painful to pass as env vars because of the transport (`.env` line format and
shell escaping mangle JSON), not env vars themselves.

## Decision (v1)

Use a repo **`.envrc`** (direnv). The **host unwraps it** with
`direnv export json` (in the repo dir), producing a JSON map of already-evaluated
`KEY → value` — JSON-safe for multiline/blobs, no shell round-trip. hounds injects
each var into the doer via Docker's `-e`/API (raw), so the doer "gathers the
unwrapped variables from the host." `direnv allow` is the operator's trust gate
(unwrapping runs the repo's `.envrc` on the host).

The **reserved namespace** ([0003](0003-trust-boundary-and-two-phase-lifecycle.md))
is stripped from the unwrapped output: `ANTHROPIC_*`, `CLAUDE_*`,
`CLAUDE_CONFIG_DIR`, `CLAUDE_CODE_TMPDIR`, `HERDR_*`, and the creds mount path —
the repo's env applies underneath, never over, these.

## Consequences

- Complex secrets stop needing hand-parsing: `direnv export json` captures them
  cleanly; injection is raw via the Docker API.
- Env is an **agent-phase** concern; the build phase stays credential-free.
  Build-time secrets (private registries) use **BuildKit `--secret`** and are a
  trusted-repo-only capability.
- Unwrapping executes repo-authored shell on the host — acceptable for repos you
  own; the `direnv allow` gate is where to revisit for untrusted repos.

## Deferred to v2

A **typed secret model** — each secret declared with a delivery type: `file`
(written to a per-task tmpfs, read-only mounted, with an optional pointer env var
— the right shape for JSON/certs/kubeconfigs) or `env` (raw via the Docker API).
The repo declares required-secret *names*; the operator supplies *values* from
central config/vault; nothing sensitive is committed. This is the hardening path
for untrusted repos and file-shaped secrets.

## Alternatives considered

- **Env-file transport / shell `echo` of blobs.** Rejected — the source of the
  parsing pain.
- **`.envrc` re-evaluated inside the doer.** Rejected — the doer lacks the host's
  secret sources; unwrap on the host instead.
