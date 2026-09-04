# 0002. Sandbox each doer in Docker inside a herdr pane, detected via HERDR_AGENT

- Status: Accepted
- Date: 2026-09-04
- Deciders: dante (with Claude)
- Verified: prototype, see [`../eval/stage1-detection.md`](../eval/stage1-detection.md)

## Context

Agents today run directly on the host in a git worktree, with full host
filesystem/process/network access — unsafe to leave running unattended. We want
each task's agent ("doer") sandboxed in its own container, **without losing
herdr's value**: peeking into the live agent from its tab, answering it, and the
engine reading its `idle/working/blocked/done` status (the engine drives off
`herdr agent wait`).

herdr identifies a pane's agent from its **foreground process**. Inside a
`docker run` that process is `docker`, so a naive containerized agent shows as
`agent: unknown` and the engine goes blind. herdr deliberately does **not**
auto-see-through wrappers (maintainer ruling R-001; herdr issues #2999, #1077),
and has no native sandbox (`execution.sandbox`/`run_as` are declarative no-ops).

## Decision

Run the agent **in a Docker container launched inside a herdr pane** (herdr stays
the execution substrate; docker is the sandbox underneath), and make herdr detect
it via the **`HERDR_AGENT` environment hint** set on the host-side `docker`
process — the maintainer-sanctioned mechanism. herdr then applies the agent's
screen manifest to the pane and derives status by screen-scanning the pane buffer,
which shows the agent TUI through the `-it` PTY.

The doer image is **agent-agnostic**: the agent CLI binary and its credentials are
**mounted at run time**, never baked. `claude` vs `codex` is chosen per run by
what's mounted plus `HERDR_AGENT=claude|codex`. Credentials are **OAuth,
copy-per-task** (a per-task copy of the host `~/.claude-personal` login, mounted
rw, discarded on teardown) — no API key, no re-login. Worktrees are created on the
host and bind-mounted at a **path-coherent** location.

## Consequences

- Sandbox (FS/process isolation) **and** full herdr functionality (peek, answer,
  liveness, collie) coexist. All four states verified through docker: idle,
  working, blocked, done.
- One image serves any agent → supports the multi-agent goal without per-agent
  images.
- Requires launching the agent under a recognized name (`claude`), pre-seeding
  folder trust for the workdir, and setting `CLAUDE_CODE_TMPDIR` to a uid-owned
  path (learned in the prototype).
- The sandbox isolates FS/process, **not credentials** — every doer holds the
  injected OAuth token (see [0003](0003-trust-boundary-and-two-phase-lifecycle.md)).

## Alternatives considered

- **Patch herdr to see through docker.** Feasible (one function in
  `src/detect/mod.rs`, Apache-2.0), but unnecessary given `HERDR_AGENT`, and it
  would fork/maintain herdr. Rejected — would also be a duplicate of ruling R-001.
- **Nested herdr-in-docker.** Heaviest; reintroduces the peek problem one layer
  down. Rejected.
- **OS-level sandbox (bwrap/nsjail) around native claude.** Possible but its own
  detection unknowns; docker-in-pane already passed the gate. Rejected for v1.
- **Bake the agent CLI/creds into the image.** Rejected: secrets in image, agent
  version coupling, loses agent-agnosticism.
