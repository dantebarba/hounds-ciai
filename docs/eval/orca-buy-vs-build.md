# Orca (onorca.dev) buy-vs-build (verdict: KEEP BUILDING — Orca is a cockpit, not a robot)

**Question:** can we drop `herdr-orchestrator` + the Docker-per-doer sandbox
([#1](https://github.com/dantebarba/hounds-ciai/issues/1),
[#2](https://github.com/dantebarba/hounds-ciai/issues/2)) and instead install
[Orca](https://www.onorca.dev) and configure agents to run the issue→PR
workflow unattended?

Evaluated 2026-09-14 against Orca `v1.4.201` (released 2026-09-13,
[releases](https://github.com/stablyai/orca/releases)), the live docs at
`onorca.dev/docs`, and the MIT-licensed source at
[github.com/stablyai/orca](https://github.com/stablyai/orca) (full source, Electron;
`src/`, `cloud/`, `mobile/`, `skills/`). Primary sources only; source code was
read wherever docs were vague.

## Summary

| # | Question | Verdict | One-liner |
| --- | --- | --- | --- |
| 1 | Autonomy (unattended issue→PR→CI→retry→escalate) | ❌ NO (⚠️ partial building blocks) | Automations are cron-only prompt launchers; orchestration is an agent-driven supervised loop; no event triggers, no GitHub gates, no automatic retry, no circuit breaker. |
| 2 | Headless Linux server | ✅ YES | `orca serve` runs on a displayless Linux box under systemd; it auto-starts Xvfb (Electron still needs a virtual X). |
| 3 | Tailscale / private network | ✅ YES | Tailscale is named as the recommended path; per-client revocable pairing tokens. |
| 4 | iOS app + CLI | ✅ YES | iOS App Store + Android APK: view status, reply to prompts, create workspaces, push on agent-finished. CLI can create worktrees, start/drive/wait on agents, run automations. |
| 5 | Sandboxing / credential isolation | ⚠️ PARTIAL | Default: agents run natively with the server's home, PATH and credentials. Optional *experimental* "Cloud VM / per-workspace environment" recipes can boot a VM/Docker container per workspace, but credentials are baked into a shared auth snapshot and you write the lifecycle scripts yourself. |

**Bottom line:** Orca covers the *cockpit* — a persistent headless runtime you
can reach from a phone over Tailscale, peek at, answer, and script. It does not
cover the *robot*: nothing in Orca watches an issue label, gates on PR
existence / CI / reviews, caps retries, or escalates. That control loop is
exactly what `herdr-orchestrator` is. See the recommendation at the end.

---

## 1. Autonomy — ❌ NO (⚠️ partial building blocks)

### What "Scheduled automations" actually are

- Definition: "Orca automations run a prompt on a schedule from the CLI, so
  recurring triage, review, and maintenance tasks can start without you opening
  a worktree by hand."
  ([docs/cli/automations](https://www.onorca.dev/docs/cli/automations))
- Triggers are **time-based only**: presets `hourly`/`daily`/`weekdays`/`weekly`,
  cron expressions, or RRULE strings. The page documents no webhook,
  issue-label, PR-event, or CI-event trigger.
  ([docs/cli/automations](https://www.onorca.dev/docs/cli/automations))
- A run = create (or select) a worktree and launch the chosen agent with the
  prompt. Source confirms: a headless run calls `createManagedWorktree` with
  `startupAgent: automation.agentId, startupPrompt: automation.prompt`
  ([src/main/automations/headless-workspace-create.ts](https://github.com/stablyai/orca/blob/main/src/main/automations/headless-workspace-create.ts)).
  Run completion is inferred by watching the run's terminal; a run that Orca
  can no longer observe is marked `dispatch_failed` with "Orca lost the terminal
  for this run before it reported completion"
  ([src/main/automations/run-completion-watcher.ts](https://github.com/stablyai/orca/blob/main/src/main/automations/run-completion-watcher.ts)).
- The only gate is a **precheck**: "Skip scheduled work when a cheap shell
  probe fails (non-zero exit records a skipped run)" — the docs' own example is
  `--precheck "gh pr list --json number -q .[0].number"`.
  ([docs/cli/automations](https://www.onorca.dev/docs/cli/automations))
- After a run nothing reads the outcome, opens a PR, retries, or escalates.
  The only documented follow-up is manual: "open that run in Orca and click
  **Rerun**". Missed schedules get a `--missed-run-grace-minutes` window.
  ([docs/cli/automations](https://www.onorca.dev/docs/cli/automations))
- Automations **do** fire on a headless server: the scheduler is constructed
  with `allowRemoteHostScheduling: state.isServeMode` and a `headlessDispatcher`
  only in serve mode
  ([src/main/startup/main-process-automations.ts](https://github.com/stablyai/orca/blob/main/src/main/startup/main-process-automations.ts)).
  Desktop clients only mirror remote-host schedules; a client that tries to run
  one gets "Remote-server automation scheduling is not available from this Orca
  client yet"
  ([src/main/automations/run-target-resolution.ts](https://github.com/stablyai/orca/blob/main/src/main/automations/run-target-resolution.ts)).

### What "Orchestration" actually is

- "Orca's structured multi-agent layer": Run (namespace + coordinator inbox),
  Task, Dispatch, messages (`worker_done`, `escalation`, `question`,
  `heartbeat`), decision gates.
  ([docs/cli/orchestration](https://www.onorca.dev/docs/cli/orchestration))
- The **coordinator is an agent (or a human) running a polling loop**, not an
  Orca daemon. The version-matched guide's role table assigns "Coordinator"
  when "The user explicitly asks to supervise, monitor, wait for results…", and
  the canonical loop is `run-create` → `worker-start` →
  `check --wait --types "worker_done,escalation,question" --timeout-ms 900000`
  repeated until every Dispatch settles.
  ([skill-guides/orchestration.md](https://github.com/stablyai/orca/blob/main/skill-guides/orchestration.md))
- "A Run is a durable namespace and coordinator inbox; it does not schedule or
  place workers." Retries are explicit (`worker-start --task <id>` for "a retry
  of a known Task"; `--retry-of <dispatchId>`); "Only positive proof of exit
  authorizes stop, abandon, or retry". There is no retry cap, no automatic
  retry, no circuit breaker.
  ([skill-guides/orchestration.md](https://github.com/stablyai/orca/blob/main/skill-guides/orchestration.md),
  [docs/cli/orchestration](https://www.onorca.dev/docs/cli/orchestration))
- **Decision gates** are questions the coordinator itself creates and resolves
  inside a task DAG (`gate-create --task … --question … --options …` /
  `gate-resolve --id … --resolution …`) — "Use a gate only for a
  coordinator-owned Task-DAG decision". They are not GitHub-state gates.
  ([skill-guides/orchestration/references/messaging-and-gates.md](https://github.com/stablyai/orca/blob/main/skill-guides/orchestration/references/messaging-and-gates.md))
- `escalation` is a message type a worker sends to the coordinator's inbox;
  the coordinator (an agent) decides what to do. No human-alert channel is
  wired to it beyond the generic agent-idle notification.
  ([docs/cli/orchestration](https://www.onorca.dev/docs/cli/orchestration))

### GitHub / Linear integration

- GitHub drawer: "Open PRs, watch checks, and triage issues without leaving the
  worktree"; create a worktree from an issue/PR; "**Fix broken checks** from the
  PR view to hand the failed check names and links to an agent"; auto-merge is
  GitHub's own. All of these are human-initiated UI actions; no label/webhook
  trigger and no auto-retry on CI failure is documented.
  ([docs/review/github](https://www.onorca.dev/docs/review/github))
- Linear: full read/write CLI (`orca linear list --filter assigned`,
  `save-issue`, `status set` …) usable *by an agent inside a prompt*.
  ([docs/cli/reference](https://www.onorca.dev/docs/cli/reference))
- Notifications fire "When an agent transitions from working to idle"; they are
  informational (system alert, sound, chip, mobile push) and trigger no action.
  ([docs/notifications](https://www.onorca.dev/docs/notifications),
  [docs/mobile](https://www.onorca.dev/docs/mobile))

### Unattended permissions

- Orca's "Yolo" agent-permission mode launches `claude` with
  `--dangerously-skip-permissions` (and the equivalent for 25 other agents),
  so an automation-launched agent *can* run without approval prompts.
  ([src/shared/tui-agent-permissions.ts](https://github.com/stablyai/orca/blob/main/src/shared/tui-agent-permissions.ts),
  [docs/settings](https://www.onorca.dev/docs/settings))

### Verdict

The closest you can get to "unattended issue→PR" with stock Orca is: a cron
automation with a `gh`-based precheck whose prompt says "pick the oldest
`agent-ready` issue, implement it, open a PR", with Yolo permissions, on a
headless server. Everything after that — was a PR actually opened, did CI go
green, was it approved, retry up to N times, stop when things are on fire, page
a human — has to live in the *prompt* (an LLM, unverified) or in scripts you
write outside Orca. Orca has no state machine, no authoritative GitHub gates,
no retry caps, no timeout-per-state, no circuit breaker.

## 2. Headless Linux server — ✅ YES

- "Use `orca serve` when the host should run without the desktop window—for
  example, a headless Linux server or a service-managed VM."
  ([docs/remote-servers](https://www.onorca.dev/docs/remote-servers))
- It is still Electron: "the packaged AppImage still needs the libraries that
  Electron expects at startup. Current Orca builds start Xvfb automatically for
  `orca serve` when no `DISPLAY` is set, but Xvfb must be installed first."
  A full apt list (libgtk-3, libnss3, libgbm1, xvfb …) is given; supported
  matrix is Ubuntu 20.04/22.04/24.04 and current Debian (glibc ≥ 2.31).
  ([docs/reference/headless-linux-server.md](https://github.com/stablyai/orca/blob/main/docs/reference/headless-linux-server.md))
- A complete systemd unit is documented (`User=orca`, `Restart=on-failure`,
  `RestartPreventExitStatus=3`, `KillMode=mixed`), plus an optional separate
  `orca-xvfb.service`. Caveat from the same doc: "Every `systemctl stop` or
  `restart` therefore ends live terminals and agent processes".
  ([docs/reference/headless-linux-server.md](https://github.com/stablyai/orca/blob/main/docs/reference/headless-linux-server.md))
- `serve --json` prints a versioned `orca_server_ready` line for supervisors.
  The repo tests this in Docker (`config/docker/headless-serve-shutdown`,
  `config/docker/headless-pairing`, both `ubuntu:24.04` + `xvfb`).
  ([config/docker/headless-pairing/Dockerfile](https://github.com/stablyai/orca/blob/main/config/docker/headless-pairing/Dockerfile))
- Linux packaging: AppImage (x64/arm64), `.deb`, `.rpm`, AUR `stably-orca-bin`;
  the CLI is `orca-ide` on Linux to avoid the GNOME screen reader.
  ([docs/install](https://www.onorca.dev/docs/install),
  [v1.4.201 assets](https://github.com/stablyai/orca/releases/tag/v1.4.201))
- Agent accounts on a headless host are registered from the server shell:
  `orca account add --agent claude`.
  ([docs/remote-servers](https://www.onorca.dev/docs/remote-servers))

## 3. Tailscale — ✅ YES

- Tailscale is named, not implied: "The easiest setup is the Orca desktop app
  on both computers, connected through Tailscale." and "Keep the server and
  client on a private network path you control, such as the same Tailscale
  tailnet or LAN." Prerequisites: "Tailscale installed on both computers /
  Both computers signed in to the same tailnet".
  ([docs/remote-servers](https://www.onorca.dev/docs/remote-servers))
- `--pairing-address` is only the *advertised* address; the listener binds
  `0.0.0.0` and the doc recommends a `100.x` Tailscale IP. Reverse-proxy URLs
  (`https://orca.example.com/runtime`) are accepted.
  ([docs/reference/headless-linux-server.md](https://github.com/stablyai/orca/blob/main/docs/reference/headless-linux-server.md))
- Auth model: "Orca creates a separate, revocable token for each paired
  client." Revoking "disconnected immediately"; "The pairing URL grants access
  to this Orca runtime. Treat it like a password." Explicit warning: "Do not
  forward the Orca port directly to the public internet. Prefer Tailscale,
  WireGuard, a trusted LAN, SSH forwarding, or an authenticated tunnel."
  Remote servers are labelled **beta**.
  ([docs/remote-servers](https://www.onorca.dev/docs/remote-servers))

## 4. iOS app / CLI — ✅ YES

### Mobile

- iOS on the App Store (`id6766130217`) plus a TestFlight channel; Android as a
  sideloaded APK (`mobile-android-v0.0.48`).
  ([docs/mobile](https://www.onorca.dev/docs/mobile))
- Positioning: "a read-mostly view of running agents … and the controls you
  actually want from a phone (replying to a prompt, sleeping a worktree,
  reviewing source control, switching agent accounts)"; "intentionally not a
  full editor — it's a remote control for the desktop you already have running".
  ([docs/mobile](https://www.onorca.dev/docs/mobile))
- Can do: see every worktree's status (working / done / waiting on input), read
  scrollback, "Send a short reply (`continue`, `yes`, free-text) when an agent
  is waiting on input", dictate, attach files, stage/commit, run saved Quick
  Commands, **create a workspace from a GitHub/Linear/GitLab issue**, and "Get
  push notifications when an agent finishes".
  ([docs/mobile](https://www.onorca.dev/docs/mobile))
- Connects over "LAN, Tailscale, or the pairing path you used", or via the
  hosted **Orca Relay** (sign-in required), and pairs with Remote Orca Servers.
  ([docs/mobile](https://www.onorca.dev/docs/mobile))

### CLI

- "Use the Orca CLI to script Orca from a terminal, manage worktrees, control
  agent terminals, automate the built-in browser, and install agent skills."
  It "ships with the desktop app" and talks to a *running* runtime (`orca
  status --json` must succeed).
  ([docs/cli/overview](https://www.onorca.dev/docs/cli/overview))
- Relevant commands: `orca serve`, `orca worktree create --agent claude`,
  `orca terminal send --text … --enter`, `orca terminal wait --for tui-idle
  --timeout-ms …`, `orca terminal read`, `orca orchestration worker-start /
  check --wait`, `orca automations create/run/runs`, `orca account add`,
  `orca environment add --pairing-code`, `orca host list`.
  ([docs/cli/reference](https://www.onorca.dev/docs/cli/reference))
- So the CLI is a viable *substrate* (equivalent to what herdr gives you:
  spawn, send, wait-for-idle, read), but it is a substrate, not a controller.

## 5. Sandboxing / credential isolation — ⚠️ PARTIAL (default: none)

### Default execution model: native, with the server's credentials

- "Install and authenticate Codex, Claude Code, OpenCode, `git`, and any
  provider CLIs on the **server computer**." / "the server needs the
  repository, tools, and credentials used by those agents".
  ([docs/remote-servers](https://www.onorca.dev/docs/remote-servers))
- Isolation is git-worktree-level only: "Every task gets its own git worktree,
  its own agent terminal, and its own browser tab."
  ([docs](https://www.onorca.dev/docs))
- With Yolo mode the agent runs `--dangerously-skip-permissions` as the `orca`
  service user with that user's full home, PATH and network.
  ([src/shared/tui-agent-permissions.ts](https://github.com/stablyai/orca/blob/main/src/shared/tui-agent-permissions.ts))
- The `docs/settings` page lists no sandbox/container toggle for ordinary
  worktrees; the only isolation surface is the experimental one below.
  ([docs/settings](https://www.onorca.dev/docs/settings))

### Optional: "Cloud VM / per-workspace environments" (experimental)

- "Each worktree can boot its own on-demand environment — a cloud sandbox, VM,
  or local Docker container — from a recipe checked into the repo (`orca.yaml`
  + lifecycle scripts)." "In the product UI this surface is labeled **Cloud
  VM** under Settings → Experimental." "Providers people wire today include
  Vercel Sandbox, Fly, Modal, plain SSH hosts, and local Docker."
  ([docs/ways-to-run](https://www.onorca.dev/docs/ways-to-run))
- **You write the create/suspend/resume/destroy scripts.** Orca is "a thin
  wrapper: your provider account, images, and billing stay yours". The
  bundled skill scaffolds `scripts/orca-vm/*.sh` and a base + auth snapshot;
  "Provisioning and building often takes 20 to 30 minutes."
  ([skill-guides/orca-per-workspace-env.md](https://github.com/stablyai/orca/blob/main/skill-guides/orca-per-workspace-env.md))
- Two connection modes: **Orca-server** (the recipe runs a second `orca serve`
  *inside* the environment and returns a pairing code) or **SSH** (recipe
  returns an SSH endpoint Orca dials). For local Docker the documented shape is
  a container running `sshd`, published on `127.0.0.1:<random>`, reached over
  SSH.
  ([skill-guides/orca-per-workspace-env/references/docker-ssh.md](https://github.com/stablyai/orca/blob/main/skill-guides/orca-per-workspace-env/references/docker-ssh.md))
- **Credential model = a shared authenticated image, not per-task copies.**
  "Authenticate once and bake it into a second snapshot layer." "Do not
  bind-mount or copy a host agent home such as `~/.codex`". Login must be the
  device-auth flow, driven by the human ("You cannot drive step 2"). "If the
  agent's credentials are short-lived, tell the user the snapshot needs
  periodic re-auth."
  ([skill-guides/orca-per-workspace-env.md §4](https://github.com/stablyai/orca/blob/main/skill-guides/orca-per-workspace-env.md))
- Git auth is a `GH_TOKEN`/`GITHUB_TOKEN`/`gh auth token` passed via
  `GIT_ASKPASS` into the environment.
  ([skill-guides/orca-per-workspace-env.md §5](https://github.com/stablyai/orca/blob/main/skill-guides/orca-per-workspace-env.md))
- Gotcha in Orca-server mode: never snapshot after `orca serve` has run, or
  "every workspace from this image shares one pairing identity".
  ([skill-guides/orca-per-workspace-env.md §3](https://github.com/stablyai/orca/blob/main/skill-guides/orca-per-workspace-env.md))

### Comparison with #1's design

| Property | Orca default | Orca Cloud VM (experimental) | herdr-orchestrator #1 |
| --- | --- | --- | --- |
| FS/process isolation per task | ❌ worktree only | ✅ VM/container per workspace | ✅ container per doer |
| Who writes the container lifecycle | — | you (scripts) | you (`DockerBackend`) |
| Agent credentials | server user's home | baked into a shared auth snapshot; human device-auth login; re-snapshot on expiry | per-task copy of host OAuth, discarded on teardown |
| Agent binary | server install | baked into image | injected from host |
| Non-root / uid mapping | service user | whatever your image does | `-u hostuid:hostgid` |
| Status detection through the boundary | Orca terminal observer (native) / second `orca serve` or SSH inside env | same | herdr `HERDR_AGENT` hint, verified |
| Status | shipping | Experimental | prototyped ([stage1-detection.md](stage1-detection.md)) |

Orca's sandbox path is real but heavier (a full second Orca runtime or sshd per
environment, 20–30 min snapshot builds, human-in-the-loop auth) and does not
give you #1's per-task credential copy or host-binary injection.

## What did NOT hold / unverified

- **UNVERIFIED — automation runs open PRs or read results.** Nothing in docs or
  source consumes a run's outcome beyond recording a status and an output
  snapshot; any "open a PR" behaviour would come from the prompt, which I did
  not test live.
- **UNVERIFIED — mobile can drive orchestration or automations.** The mobile
  doc lists status, reply, create-workspace and push; it does not mention
  automations or orchestration commands.
- **UNVERIFIED — `orca serve` behaviour on non-Debian distros.** The supported
  matrix is Ubuntu 20.04–24.04 and current Debian only.
- **Did not hold — "agents run natively on the server" is the only model.**
  That is the default, but the experimental Cloud VM path does exist; the user
  prompt's framing was incomplete.
- **Did not hold — orchestration "gates" are GitHub gates.** They are
  coordinator-owned DAG questions; no `github_pr`/`github_checks`/
  `github_reviews`/`github_mergeable` equivalent exists anywhere in Orca.
- **Not evaluated:** Orca Relay's security model (hosted, sign-in required);
  Windows/macOS server behaviour; the `hermes` external-automation manager
  integration (`src/main/automations/hermes-cron-*.ts`) beyond noting it mirrors
  a third-party cron tool's jobs into the Automations table.

## Bottom line — build vs buy

**Verdict: keep building the orchestrator + sandbox. Consider Orca only as an
optional operator cockpit on top, not as a replacement.**

What Orca covers (well):

1. A persistent, headless, systemd-managed agent runtime on a Linux box.
2. Reaching it over Tailscale with revocable per-device tokens.
3. Peek/answer/create-workspace from an iOS app, with push on agent-finished.
4. A CLI that can spawn an agent in a worktree, send text, wait for idle, read
   the screen — i.e. the same substrate role herdr plays for you today.

What Orca does not cover (the gap):

1. **Event-driven intake.** No webhook / issue-label / PR-event trigger; only
   cron + shell precheck.
2. **The validated state machine.** No `implementing → pr_open →
   changes_requested → escalated` graph, no authoritative GitHub gates, no
   per-state timeouts, no retry caps, no circuit breaker, no
   `needs_human` escalation channel. Orchestration's coordinator is *an
   agent running a polling loop*, so "gates", "retries" and "escalation" there
   are LLM-judged, not deterministic.
3. **Per-task sandbox with per-task credential copies.** Default execution is
   native with the server's credentials and Yolo permissions. The experimental
   Cloud VM path gives per-workspace isolation but with a shared baked-in auth
   snapshot, human device-auth, and lifecycle scripts you author — it is a
   different (heavier) design than #1, not a drop-in.

Can the gap be closed by configuring Orca? Only partially and only by moving
the logic into prompts or external scripts: a cron automation with a `gh`
precheck and a Yolo `claude` prompt gets you "an agent tries an issue every
hour". Everything deterministic that you have already built — schema-validated
workflow, gate evaluation against GitHub, retry caps, timeouts, escalation,
health/event log — would have to be rewritten as scripts driving `orca
terminal … --json`, which is precisely `herdr-orchestrator` with a different
backend. That is a backend swap, not a reason to stop.

Practical recommendation:

1. Proceed with #1/#2 (Docker-per-doer on herdr). Nothing in Orca supersedes
   it.
2. If you later want the phone/cockpit experience, the seam already exists:
   `exec.ExecutionBackend` could gain an `OrcaBackend` wrapping `orca worktree
   create --agent`, `orca terminal send/wait/read` (all `--json`), running
   against a headless `orca serve`. That would give iOS peek/answer and
   Tailscale pairing for free while the Go engine keeps owning the state
   machine and gates.
3. Do not adopt Orca's Cloud VM recipes for the sandbox: they are experimental,
   require a second Orca runtime or sshd per task, and bake credentials into a
   shared image — weaker than #1's per-task credential copy.
