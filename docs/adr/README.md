# Architecture Decision Records

Decisions behind **hounds** — the "ticket → PR" orchestrator built on this fork.
Each ADR records the context, the decision, its consequences, and the alternatives
rejected. Written 2026-09-04 from a design grilling; prototype-backed where noted.

| # | Decision |
|---|----------|
| [0001](0001-adopt-and-extend-herdr-orchestrator.md) | Adopt & extend herdr-orchestrator (Go fork `hounds-ciai`, keep upstream module path) |
| [0002](0002-docker-per-doer-sandbox-via-herdr-agent.md) | Docker-per-doer sandbox inside a herdr pane, detected via `HERDR_AGENT` (agent-agnostic image, OAuth copy-per-task) — *prototype-verified* |
| [0003](0003-trust-boundary-and-two-phase-lifecycle.md) | Trust boundary (repos select, don't author) + two-phase container lifecycle (cred-free build, cred-injected agent) |
| [0004](0004-host-side-workflow-library.md) | Workflows = host-side Go library emitting `*config.Workflow`; repos select by name; CLI-first |
| [0005](0005-environment-provisioning-dockerfile.md) | Environment = repo `.hounds/Dockerfile`, built by the orchestrator, hounds-wrapped overlay, warm image; `hounds init` scaffolds |
| [0006](0006-project-env-and-secrets-envrc.md) | Project env/secrets via `.envrc` unwrapped host-side (`direnv export json`); typed file-secrets deferred to v2 |
| [0007](0007-v1-workflow-plan-implement-review-promote.md) | v1 workflow: plan → implement → review(+fixes) → promote (reviewer is the gate, no local tests) |
| [0008](0008-reporting-and-pr-decision-log.md) | Agents report via a per-task callback API; a summarizer writes the single PR decision comment |
| [0009](0009-hitl-via-collie-and-escalation.md) | HITL via collie (herdr-only, no Claude hook); escalation policy — *`blocked`-state verified* |
| [0010](0010-config-layering-and-v1-scope.md) | Central vs `.hounds/` config layering, workspace/branch/concurrency, and v1 scope |

Supporting evidence: [`../eval/stage1-detection.md`](../eval/stage1-detection.md)
(the Docker-doer detection probe).
