# 0009. Human-in-the-loop via collie (herdr-only); escalation policy

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)
- Verified: `blocked` state through docker, see [`../eval/stage1-detection.md`](../eval/stage1-detection.md)

## Context

Agents will sometimes need a human. We want mobile peek/answer without building a
notification stack, and without any component needing a direct line to Claude
(which the container boundary would complicate).

## Decision

Use **collie** as the HITL surface. collie is a **herdr-only** client (its docs:
"not tied to Claude CLI"; it talks to the multiplexer via snapshot polling + pane
keys, and pushes on pane-`blocked`). It requires **no Claude hook** and no
orchestrator↔collie integration:

```
agent blocks → herdr pane.agent_status_changed → collie web-push (PWA/VAPID)
   → human answers via collie → keystrokes typed into the herdr pane (reach the container)
```

Because collie only touches herdr, the container boundary is invisible to it —
verified: herdr reports `blocked` for a containerized claude at a prompt.

**Escalation policy:**
- **Blocked tabs → no orchestrator action** (human/collie territory; collie's
  push notifies).
- **Finished tab but no report to the orchestrator** → inject a **deterministic
  "report your status" prompt** to elicit the callback before advancing.
- Prompts bias **resolve-when-able** (signal for a human only after exhausting
  autonomous options).
- Failures are **never silent**: on give-up → `agent:failed` terminal with a
  draft PR (or issue) comment describing the failure. Label lifecycle:
  `agent:<label> → agent:running → agent:done | agent:failed`.

## Consequences

- hounds inherits mobile push + HITL for free; the only requirement (met) is that
  doers surface state to herdr.
- Operator setup cost: collie running on the tailnet, subscribed. Not hounds code.

## Alternatives considered

- **A VAPID/web-push Claude hook.** Unnecessary — collie already delivers
  web-push on herdr events. Parked as a research spike, not v1.
- **Orchestrator routes questions to collie.** Unnecessary — collie is latched to
  herdr directly.
