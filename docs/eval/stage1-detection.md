# Stage 1 — Docker-per-doer detection probe (verdict: PASS)

**Question:** can a `claude` running inside a Docker container, launched inside a
herdr pane, be (a) authenticated with host OAuth creds and (b) detected by herdr
as a live agent — so the orchestrator engine keeps its working/blocked/done
signal?

## Result — VIABLE, no herdr changes needed

| Aspect | Result |
| --- | --- |
| OAuth injection (no re-login, no API key) | ✅ works |
| Interactivity (keys reach the agent) | ✅ works |
| **herdr detects the agent (`agent_status`)** | ✅ works, via `HERDR_AGENT` hint |
| **Live status lifecycle (idle → working → done)** | ✅ works through docker |
| **`blocked` state at an interactive prompt** | ✅ works through docker (verified: `working → blocked` when claude hit an AskUserQuestion prompt) |

**Conclusion: docker-in-herdr-pane is viable.** The agent runs sandboxed in a
container, you peek/answer via the tab as usual, and herdr reports full status —
which is exactly what the engine drives on (`herdr agent wait --until done`).

## The one lever: `HERDR_AGENT`

herdr identifies a pane's agent from the **foreground process** name. Inside a
`docker run` the foreground process is `docker`, so herdr can't recognize it —
**and this is deliberate** (maintainer ruling R-001: no automatic process-tree /
wrapper fallback; see herdr issues #2999, #1077). The supported mechanism is an
environment hint: set **`HERDR_AGENT=claude` on the host-side wrapper process**
(the `docker` process itself), and herdr uses claude's screen manifest for that
pane. State then comes from screen-tail scanning of the pane buffer, which shows
claude's TUI through docker's `-it` PTY.

herdr reads the hint from `/proc/<pid>/environ` of the foreground job members
(`platform::parse_agent_env_hint`, exact var `HERDR_AGENT`, value parsed as an
agent label). Detection fires on the next detection re-check after the container
starts (a few seconds), not instantly.

### Working launch (verified)

```sh
HERDR_AGENT=claude docker run -it --rm -u 1000:1000 \
  -v ~/.local/share/claude:/opt/claude:ro \
  -v <per-task-config-copy>:/home/agent/.claude:rw \
  -v <worktree>:/work -w /work \
  -e HOME=/home/agent -e CLAUDE_CODE_TMPDIR=/home/agent/tmp \
  -e CLAUDE_CONFIG_DIR=/home/agent/.claude \
  <doer-image> sh -c 'mkdir -p /home/agent/tmp && exec claude'
```

`HERDR_AGENT` is set on the **docker** process (host side), NOT passed with `-e`
into the container. Verified: pane transitions `idle → working → done` in
`herdr agent list` / `agent wait`.

## Requirements / gotchas learned

- **Auth:** `~/.claude-personal/.credentials.json` (OAuth `claudeAiOauth`) mounted
  into the container authenticates with no login and no API key. Use
  copy-creds-per-task (mount a copy rw), not the live host file.
- **Trust prompt:** claude blocks on the folder-trust dialog. Pre-seed the
  mounted `.claude.json` with a trusted project entry for the container workdir
  (`hasTrustDialogAccepted: true`), or claude never reaches its agent UI.
- **Tmpdir:** claude refuses a root-owned `/tmp`; set `CLAUDE_CODE_TMPDIR` to a
  uid-1000 path inside the container.
- **Agent name:** invoke the agent as `claude` (recognized name). The versioned
  binary basename (`2.1.260`) is not needed for identity here because
  `HERDR_AGENT` drives the manifest, but keep launch clean.

## What did NOT work (for the record)

- Plain docker-in-pane with no hint → `agent_not_found`.
- Wiring the herdr claude hook + socket + `HERDR_ENV`/`HERDR_PANE_ID` into the
  container: the socket is reachable and the hook runs, but herdr does **not**
  use the hook's `report_agent_session` as an identity fallback (see #803), so the
  pane stayed unidentified. `HERDR_AGENT` is the correct lever, not the hook.
- herdr has no native sandbox; the orchestrator's `execution.sandbox`/`run_as`
  are declarative no-ops.
