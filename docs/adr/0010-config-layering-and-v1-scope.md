# 0010. Config layering (central vs `.hounds/`), control surface, and v1 scope

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)

## Context

Two config layers exist: what the operator controls centrally, and what a repo
carries. To read a repo's config you must first clone it — which you can't do
without knowing the repo. And the trust boundary
([0003](0003-trust-boundary-and-two-phase-lifecycle.md)) requires central config
to be able to cap what a repo asks for.

## Decision

**Central operator config** (self-sufficient, CLI-managed — e.g.
`~/.hounds/hounds.yaml`): the project list (`owner/repo` + tracker), clone-pool
path, global `max_concurrent_tasks`, credentials source
(`agent_config_dir: ~/.claude-personal`), poll interval; tracker auth via the
logged-in `gh`. CLI verbs add/remove repos and set caps.

**Per-repo `.hounds/`** (mandatory since the environment can't be inferred,
[0005](0005-environment-provisioning-dockerfile.md)): `Dockerfile`, `config.yaml`
(trigger labels, workflow name, `test`/review knobs, optional `branch_strategy`),
optional `AGENTS.md`.

**Precedence:** central sets defaults + hard caps; the repo overrides **only
within** those caps. Absent a repo value → central default.

**Control surface:** **CLI-first**; HTTP control/config API deferred (the doer
report endpoint, [0008](0008-reporting-and-pr-decision-log.md), is the only v1
HTTP).

**Workspace layout:** a managed clone pool `~/hounds-projects/<owner>/<repo>`
(built once → doer image), per-task worktrees under
`<pool>/<owner>/<repo>/.worktrees/issue-<n>`, each bind-mounted into the doer
**path-coherently**. **Branch strategy** (sandcastle-style `head | merge-to-head |
branch`) defaults to **`branch`** (`agent/issue-<n>`), set on the workflow with an
optional per-repo override.

**Concurrency:** a single global cap for v1 (reuse the fork's one-poller/N-workers/
single-writer scheduler); per-project/per-tracker caps deferred.

## v1 scope

**In:** claude only; one GitHub repo; one workflow; Docker doer; plan → implement
→ review → promote; report API; collie HITL.
**Deferred:** merge-gate/auto-merge, multi-agent (codex), multi-tracker + a
`TicketProvider` abstraction (use the fork's `github.Client` directly until a
second tracker lands), full HTTP API, typed file-secrets
([0006](0006-project-env-and-secrets-envrc.md)), CI-status reading, VAPID Claude
hook, the LLM unblocker.

## Consequences

- A repo works with central config alone for wiring; `.hounds/` is required only
  because the *environment* can't be centralized or inferred.
- No abstraction is built before it has a second implementation (YAGNI on
  `TicketProvider`), diverging from the original "pluggable day one" plan.

## Alternatives considered

- **Optional `.hounds/`.** Rejected: the environment build can't be inferred or
  centralized, so the repo must carry it.
- **HTTP-first control.** Rejected: CLI-first is simpler for v1; HTTP follows.
