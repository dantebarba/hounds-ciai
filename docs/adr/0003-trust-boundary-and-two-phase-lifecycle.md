# 0003. Trust boundary: repos select behavior, and a two-phase container lifecycle

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)

## Context

hounds runs agents with the operator's injected OAuth credentials. If a watched
repo could author *what the agent does*, a repo you don't fully control would be
authoring instructions that run with your token — and the Docker sandbox isolates
filesystem/process but **not** credentials. The environment build, however, is
inherently repo-specific (see [0005](0005-environment-provisioning-dockerfile.md))
and must run repo-authored steps.

## Decision

**Repos select behavior from a host-registered menu; they never author it.**
Workflow definitions live host-side ([0004](0004-host-side-workflow-library.md));
a repo's `.hounds/` only *selects* a workflow by name and sets bounded overrides
(e.g. a model from an allowlist). Central operator config sets defaults and hard
caps; the repo overrides only within those caps.

Split the container lifecycle into **two phases**:

1. **Build phase** — build the doer image from the repo's `.hounds/Dockerfile`.
   Runs repo-authored build steps, but **credential-free** (no agent OAuth token
   present).
2. **Agent phase** — run the agent with credentials + project env injected, after
   the image exists.

A **reserved env namespace** protects the credential/detection channel: the
repo's env can never set `ANTHROPIC_*`, `CLAUDE_*`, `CLAUDE_CONFIG_DIR`,
`CLAUDE_CODE_TMPDIR`, `HERDR_*`, or the creds mount path (see
[0006](0006-project-env-and-secrets-envrc.md)).

## Consequences

- A hostile repo's blast radius shrinks to "picked a different allowed model" —
  it cannot exfiltrate the OAuth token via workflow definition, and its build
  steps run tokenless.
- Build-time secrets (private registries) pierce the credential-free build; they
  are a **trusted-repo-only** capability via BuildKit `--secret`
  ([0006](0006-project-env-and-secrets-envrc.md)).
- Credential *isolation* between doers is explicitly out of scope for v1 — all
  doers share the operator's personal OAuth token (an intentional, accepted
  trade; the personal account is separate from the operator's work account).

## Alternatives considered

- **Let repos author workflows (e.g. `.hounds-ci.ts`).** Rejected: repo-shipped
  executable config running with your creds is the exact risk we're avoiding.
- **Run the whole lifecycle in one phase with creds present.** Rejected: repo
  build steps would run with the OAuth token in scope.
