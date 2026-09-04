# 0004. Workflows are a host-side Go library; repos select them by name

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)

## Context

Inspired by sandcastle's config-as-code (`sandcastle.run({agent, sandbox,
branchStrategy})`), we want expressive, testable, refactorable workflow
definitions. But config-as-code shipped *in a watched repo* is arbitrary code
running with our credentials (see [0003](0003-trust-boundary-and-two-phase-lifecycle.md)).
The fork's engine consumes a parsed `*config.Workflow` **directly** — there is no
separate compiled graph; `config.Load` unmarshals YAML into `*config.Workflow` and
the engine interprets it live.

## Decision

Workflow definitions are a **host-side Go library**, compiled into the daemon
(config-as-code, authored by the operator). A fluent builder
(`workflows.New().For(labels).Step("plan").Model("opus").Effort("high")…`) **emits
a `*config.Workflow`** — the exact struct the YAML path produces — so the existing
engine, validator, and safety invariants are reused unchanged.

**Repos select a workflow by name** via their `.hounds/` config (plus bounded
overrides). Repos never define workflow behavior.

The control surface is **CLI-first** for wiring/selection (which repos/trackers,
caps, enable/disable); an HTTP control API is deferred (except the doer report
endpoint, [0008](0008-reporting-and-pr-decision-log.md)).

## Consequences

- Full type-safety/ergonomics for the trusted author (operator); the engine
  underneath is proven.
- **Changing a workflow's steps is a recompile-and-redeploy**, not a runtime edit.
  Accepted: workflows are stable and versioned with the daemon.
- Per-task recovery re-parses a YAML snapshot (`config.Parse`); a pure-Go builder
  must either serialize to round-trippable YAML for snapshotting or that recovery
  path is adapted. Tracked as an implementation note.
- Model/effort become **structured role fields** the builder sets and
  `launchArgs` translates per-launcher (mirroring the existing `--allowedTools`
  pattern): `claude --model <m> --effort <low|medium|high|xhigh|max>`.

## Alternatives considered

- **Declarative YAML workflows (the fork's current model).** Fine, but loses the
  builder ergonomics; kept underneath as the emitted representation.
- **Embedded sandboxable language (Starlark/CUE) for runtime-authored workflows.**
  Rejected for v1 by "all in Go"; revisit only if runtime authoring is needed.
- **Bake model/effort into the raw `launch:` array.** Rejected: unvalidated,
  leaks CLI specifics into config.
