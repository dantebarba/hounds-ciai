# 0005. Environment is a repo-committed `.hounds/Dockerfile`, built by the orchestrator

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)

## Context

Doers must run in a ready environment (deps installed, buildable) so agents don't
waste effort re-bootstrapping or do it wrong. hounds cannot *infer* how to build
an arbitrary repo (runtime auto-detection is unreliable — the reason
devcontainers/buildpacks exist), and build deps must **not** be required on a
developer's local machine (all building is containerized). The environment is
repo-specific and should version with the code.

## Decision

Each repo commits a **`.hounds/Dockerfile`** describing its *project* environment.
The **orchestrator builds it** (`docker build -f .hounds/Dockerfile .`, context =
repo root) — never on a developer machine. hounds **auto-wraps** the built image
with a thin, hounds-owned **agent-runtime overlay** (ripgrep, gh, a uid-matching
non-root user, entrypoint), so the dev's Dockerfile stays project-only. The built
image is the **warm layer**: deps baked in, so per-task worktrees mount only
*source*. The image is shared across all doers for the project; rebuilt when the
Dockerfile or declared manifests change (content hash).

`.hounds/` is **mandatory** — a repo is hounds-enabled by running **`hounds init`**
(a distributed, daemon-independent CLI) which scaffolds `.hounds/{Dockerfile,
config.yaml, AGENTS.md}`, **seeded from stack detection at init time** (a first
draft the dev reviews/edits and commits). Detection is an init-time convenience,
never runtime magic.

## Consequences

- No local toolchain needed — devs author a text file; the orchestrator builds it.
- Build cost is paid once per project, reused across tasks.
- Building a repo-authored Dockerfile runs repo instructions on Docker; for v1
  this runs on the host daemon (trusted repos). An isolated/rootless builder is
  the path when watching untrusted repos.
- Alpine/musl bases break the glibc-linked mounted agent binary — detect and flag
  (Debian/Ubuntu bases, the common case, are fine).

## Alternatives considered

- **Devcontainer-first / depend on devcontainers.** Rejected as a dependency;
  offered only as an init-time convenience (base `env` on an existing
  `.devcontainer`/`Dockerfile` if present).
- **Runtime auto-detection of build steps.** Rejected — unreliable.
- **A `setup.sh` run locally.** Rejected — requires local build deps.
