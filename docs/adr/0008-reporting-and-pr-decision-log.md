# 0008. Agents report via a callback API; a summarizer writes the PR decision log

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)

## Context

Decisions and resolutions made during a task must be recorded as **GitHub PR
comments** (the durable, GitHub-is-truth audit trail). But the PR doesn't exist
until after `implement`, and we want the agent's own conclusions captured, not
just lifecycle events.

## Decision

The environment-contract prompt instructs each agent to **POST its conclusions /
decisions / reports to the orchestrator via a callback** (curl to a small HTTP
endpoint). The orchestrator exposes a **minimal per-task report endpoint**; each
doer gets `HOUNDS_REPORT_URL` + a per-task token injected as env and reaches the
host via Docker's host-gateway. Reports accumulate in the store, keyed by task.
**This report-ingest endpoint is the only HTTP surface in v1** (the full
control/config API stays deferred, [0004](0004-host-side-workflow-library.md)).

At the **promote** stage, a **lightweight orchestrator-invoked agent** (cheap
model) reads the accumulated reports + the diff and produces one summary; the
**orchestrator posts it** as the PR comment (single writer). Reports made before
the PR exists are buffered and flushed as the PR's opening comment(s).

## Consequences

- Rich, agent-authored reasoning is captured continuously, decoupled from PR
  comment formatting.
- Single-writer PR comments (orchestrator) keep format consistent and controlled.
- Requires doer→host networking (host-gateway URL + per-task token).

## Alternatives considered

- **Agents post PR comments directly via `gh`.** Rejected for v1: weaker control,
  format drift, and awkward pre-PR ordering.
- **Lift decisions only from `PLAN.md`/output at the end.** Rejected: loses
  in-flight reasoning and requires the orchestrator to parse artifacts.
