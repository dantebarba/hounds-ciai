# 0007. v1 workflow: plan → implement → review(+fixes) → promote

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)

## Context

The v1 slice must run start-to-finish for one repo, one workflow, **claude only**.
Running a repo's test suite in a separate container per task is costly, and the
fork's "GitHub is source of truth" model already favours GitHub-side signals over
local execution.

## Decision

The v1 workflow (emitted by the host-side builder into the engine's state-graph):

```
plan      : spawn planner   → writes PLAN.md, commits            → implement
implement : spawn implementer → codes, commits, pushes branch,
                                opens a DRAFT PR                   → review
review    : spawn reviewer   → reviews the diff, APPLIES FIXES
                                (commits/pushes)                   → promote
promote   : orchestrator flips draft → ready, posts a summarized
                                PR comment                         → done
```

- **The reviewer agent is the quality gate** — code review with fixes, **not** a
  local test run. (Reading GitHub Actions status is a later add.)
- **The orchestrator opens/promotes the PR** (so it controls draft-vs-ready),
  a deliberate change from the fork where the agent opens it.
- Handoff between stages is via **worktree artifacts** (`PLAN.md`, the diff), no
  shared conversational context. Each stage is its own sandboxed doer with its own
  model/effort ([0004](0004-host-side-workflow-library.md)).
- Each stage's context carries a host-authored **environment contract**
  ("workspace at /work, on branch X, deps installed, don't re-setup…", bias:
  **resolve-when-able**) plus optional `.hounds/AGENTS.md`.

## Consequences

- No per-task test container → lower cost; quality rests on the reviewer stage.
- A draft PR exists from `implement` onward, giving the reviewer, the human, and
  any GitHub Actions a real surface.

## Alternatives considered

- **A `validate` stage that runs the repo's tests in a doer.** Rejected as too
  costly for v1; replaced by review-with-fixes.
- **Agent opens the PR (fork default).** Rejected: the orchestrator needs to
  control draft-vs-ready based on the review outcome.

## Out of scope (v1)

Merge gate / auto-merge, triage/intake stage, multi-agent (codex), multi-tracker,
the LLM unblocker, CI-status reading.
