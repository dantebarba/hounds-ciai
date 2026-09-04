# 0001. Adopt and extend herdr-orchestrator (Go fork) as the base for hounds

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)

## Context

hounds is a "ticket → PR" orchestrator: a background daemon that picks up issues
from trackers and drives them to pull requests with AI agents, in stages. Before
building from scratch, a market/codebase scan found `sean1588/herdr-orchestrator`:
a real, non-slop Go daemon (~7k LOC + ~8k tests) that already covers ~4/6 of the
target surface — label-driven polling, a declarative state-graph engine, a
reviewer role, SQLite single-writer store, and an `ExecutionBackend` seam — and is
built on the same `herdr` this machine already runs. Its license was confirmed
MIT.

## Decision

Fork `sean1588/herdr-orchestrator`, rename it **`hounds-ciai`**, and build hounds
by **extending** it rather than reimplementing. Keep the work in **Go**. **Keep
the upstream Go module path** (`github.com/sean1588/herdr-orchestrator`) instead
of renaming it to the fork.

## Consequences

- We inherit a working engine, validator, store, herdr backend, and test suite.
- Keeping the upstream module path means `git merge upstream/main` does not
  conflict on every import line — we can track upstream while we diverge.
- We commit an MIT `LICENSE` into the fork (upstream shipped none at fork time);
  provenance credits the original author.
- The engine's model becomes our constraint: extensions must fit its
  `*config.Workflow` state-graph or explicitly replace a seam.

## Alternatives considered

- **Build from scratch in Go, using it only as reference.** Rejected: it already
  works and is close; rebuilding buys little and loses the test suite.
- **Use sandcastle (TypeScript) as the execution core.** Rejected: stack pivot,
  and it's a library, not a daemon — it deliberately omits the tracker-polling,
  multi-repo, declarative layer hounds needs (see [0004](0004-host-side-workflow-library.md)).
- **Rename the Go module to the fork.** Rejected: breaks upstream mergeability for
  a cosmetic gain (it's an internal import, invisible to users).
