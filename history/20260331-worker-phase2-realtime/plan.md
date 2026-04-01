---
title: "Phase 2: Real-Time Agent-Supervised Worker Execution"
status: completed
priority: high
effort: large
tags: [team, worker, websocket, streaming, claude-cli, sdk-url]
created: 2026-03-31
completed: 2026-04-01
blockedBy: ["history/20260328-worker-phase1"]
---

# Phase 2: Real-Time Agent-Supervised Worker Execution

## Problem

Phase 1 flow is **blind**: worker runs `claude --print` one-shot, agent only sees results
after completion. No visibility into tool calls, no ability to cancel, inject context, or
answer questions mid-run. Long-running tasks (30min+) are fire-and-forget.

## Goal

Agent (Zoro/Sanji) **supervises Claude Code execution in real-time** on the Windows worker:
- Sees streaming output (text, thinking, tool calls, tool results) as they happen
- Can cancel a run mid-execution (`interrupt`)
- Can inject follow-up messages mid-run (answer questions, add context)
- Can approve/deny tool permissions remotely
- Dashboard shows live execution progress

## Key Discovery: `--sdk-url` Protocol

Claude Code CLI has a **hidden `--sdk-url` flag** that makes it connect as a WebSocket
client to a server you control. Protocol is NDJSON — same format as stdin/stdout stream-json.
This is the officially supported programmatic control path (used by Claude Code Web UI,
The Vibe Companion, and the Agent SDK internally).

```bash
claude --sdk-url wss://goclaw.example.com/v1/worker/stream/<task-id>?token=<jwt> \
       --print \
       --output-format stream-json \
       --input-format stream-json \
       --dangerously-skip-permissions \
       -p ""
```

The CLI:
1. Connects to the WS URL as a **client**
2. Sends `system/init` with session_id, tools, model info
3. Waits for a `user` message from the server (the task prompt)
4. Streams `assistant`, `stream_event`, `tool_progress`, `result` messages back
5. On `can_use_tool` control_request → server responds with allow/deny
6. Server can send `interrupt` to cancel, or additional `user` messages for follow-up

## Architecture

```
┌──────── Windows Worker ──────────┐     ┌──────── VPS (GoClaw) ──────────┐
│                                   │     │                                 │
│  PowerShell Worker Script         │     │  WorkerStreamHandler            │
│  ┌────────────────────────┐       │     │  (HTTP→WS upgrade, auth,       │
│  │ claim task via HTTP    │       │     │   session lifecycle)            │
│  │ launch: claude         │       │     │  ┌───────────────────────────┐  │
│  │   --sdk-url wss://...  │  WS   │     │  │ WorkerSession             │  │
│  │   --print              │◄─────►│     │  │ - NDJSON parse/emit       │  │
│  │   --output-format ...  │       │     │  │ - event ring buffer       │  │
│  │   --input-format ...   │       │     │  │ - permission auto-approve │  │
│  │   --dangerously-skip.. │       │     │  │ - agent msg channel       │  │
│  │   -p ""                │       │     │  └────────┬──────────────────┘  │
│  └────────────────────────┘       │     │           │                     │
│  (Claude CLI is WS client)        │     │  ┌────────▼──────────────────┐  │
│                                   │     │  │ Agent Loop (event bus)    │  │
│  Worker only: claim, launch,      │     │  │ - receives stream events  │  │
│  wait for exit, submit/fail       │     │  │ - can inject messages     │  │
└───────────────────────────────────┘     │  │ - can interrupt/cancel    │  │
                                          │  └────────┬──────────────────┘  │
                                          │  ┌────────▼──────────────────┐  │
                                          │  │ Dashboard (WS events)     │  │
                                          │  │ - live execution viewer   │  │
                                          │  └──────────────────────────┘  │
                                          └────────────────────────────────┘
```

## NDJSON Wire Protocol (Summary)

### CLI → VPS (Worker → GoClaw)
| Type | Purpose |
|------|---------|
| `system` (subtype=init) | First msg: session_id, tools, model, version |
| `assistant` | Full assistant message (content blocks) |
| `stream_event` | Token-by-token chunks (with `--verbose`) |
| `tool_progress` | Tool execution heartbeat (tool_name, elapsed_time) |
| `tool_use_summary` | Summary of tool executions |
| `result` | Query complete (success/error, cost, usage) |
| `control_request` (can_use_tool) | Permission request |
| `keep_alive` | Keepalive ping |

### VPS → CLI (GoClaw → Worker)
| Type | Purpose |
|------|---------|
| `user` | Send prompt or follow-up message |
| `control_response` | Respond to permission request (allow/deny) |
| `control_request` (interrupt) | Abort current turn |
| `control_request` (end_session) | Gracefully terminate |
| `keep_alive` | Keepalive pong |

## Rollout Strategy: 2 Sub-Phases

### B1: Output Streaming + Cancel + Auto-Permissions (MVP)

- VPS WS endpoint accepts Claude CLI connection
- Sends task prompt as first `user` message
- Receives and buffers all stream events
- Auto-approves all `can_use_tool` (we already use `--dangerously-skip-permissions`)
- Agent can `interrupt` to cancel run
- Broadcasts events to dashboard via event bus
- On `result` → auto-submit task for review
- **No agent-initiated follow-up messages yet**

### B2: Bidirectional Agent Control

- Agent can inject `user` messages mid-run via `team_tasks(action="inject_message")`
- Agent can answer Claude's questions in real-time
- Agent sees tool calls before execution and can deny specific ones
- Requires expanding agent loop to consume real-time stream events

## Phases

- [phase-01](phase-01-ws-endpoint.md) — WS endpoint + WorkerSession + NDJSON handler
- [phase-02](phase-02-worker-launch.md) — Worker script: launch claude with --sdk-url
- [phase-03](phase-03-event-bus.md) — Broadcast stream events to agent + dashboard
- [phase-04](phase-04-agent-actions.md) — Agent interrupt/cancel via team_tasks tool
- [phase-05](phase-05-dashboard-viewer.md) — Dashboard live execution viewer (UI)
- [phase-06](phase-06-bidirectional.md) — B2: Agent injects messages, permission control

## Security

- **Auth**: Worker provides JWT/API key on WS upgrade; validated same as HTTP worker endpoints
- **Scope**: Each WS connection is bound to a specific task_id; task must be in_progress and owned by the connecting worker
- **SSRF**: `--sdk-url` only accepts the specific GoClaw endpoint URL — worker constructs it from config
- **Timeout**: Max WS connection lifetime matches task timeout (max_runtime_seconds)
- **Reconnection**: Claude CLI has built-in WS reconnect (3 attempts, exponential backoff). VPS buffers last 1000 messages for replay via `X-Last-Request-Id`

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| `--sdk-url` is hidden/undocumented | MEDIUM | Proven by The Vibe Companion, Agent SDK uses same protocol. CLI validates the flag exists. |
| PowerShell launching claude with --sdk-url | LOW | PS only starts process, Claude handles WS natively |
| WS through nginx reverse proxy | LOW | nginx already handles WS for gateway; same config |
| Long-lived WS connections (30min) | MEDIUM | Keepalive ping/pong, heartbeat, connection timeout |
| Event floods from verbose output | LOW | Rate-limit dashboard broadcast, buffer events |
| Claude CLI version incompatibility | MEDIUM | Pin min version in worker config, check `claude_code_version` from init |

## Pre-Implementation Spike

Before Phase 01, verify on Windows:
```powershell
# Test: does Claude CLI connect to a local WS server?
# Start a tiny WS echo server (node/bun), then:
claude --sdk-url ws://localhost:9999 --print --output-format stream-json --input-format stream-json -p ""
```

Verify:
1. CLI connects as WS client ✓
2. CLI sends `system/init` as first message ✓
3. Sending `user` message triggers Claude response ✓
4. `result` message received at end ✓
5. `--dangerously-skip-permissions` suppresses `can_use_tool` requests ✓

### Spike Results — 2026-03-31 ✅ ALL PASSED

Tested with `scripts/spike_worker_ws.go` (Go WS server) + Claude CLI 2.1.76 on WSL.

**Protocol sequence confirmed:**
```
1. Claude CLI connects as WS client                                    ✅
2. Server sends control_request{initialize}                            ✅
3. CLI sends hook_started/hook_response (SessionStart hooks)           ✅
4. CLI sends control_response{success} with models, commands           ✅ (5 models)
5. CLI sends system/init (session_id, model, tools, permissionMode)    ✅
6. Server sends user message (task prompt)                             ✅
7. CLI sends assistant/thinking                                        ✅ (91 chars)
8. CLI sends assistant/tool_use (Bash: ls -la)                         ✅ (with tool input JSON)
9. CLI sends user/tool_result (tool output back from execution)        ✅
10. CLI sends assistant/text (summary)                                 ✅
11. CLI sends result/success (cost=$0.17, turns=2, 15s)                ✅
12. CLI closes WS (1000 normal) → auto-reconnects → replays session   ✅
```

**Key findings:**
- **Session reuse**: CLI reconnects with same session_id after result — protocol expects
  server to stay alive for multi-turn. Server must NOT close after first result.
- **`initialize` is required**: Without it, CLI sends hooks but never sends `system/init`.
  Sequence: server→`initialize` → CLI→`control_response` → CLI→`system/init`.
- **`bypassPermissions`**: With `--dangerously-skip-permissions`, no `can_use_tool` requests
  are sent. Tool execution is fully autonomous.
- **Tool result routing**: CLI sends tool results as `user` type messages with
  `tool_result` content blocks — these are self-contained, no server intervention needed.
- **Cost tracking**: Result includes `total_cost_usd`, `num_turns`, `duration_ms` —
  perfect for task metadata enrichment.
- **8 messages per simple task**: init→thinking→tool_use→tool_result→text→result.
  Manageable event volume even without coalescing.

**Spike code**: `scripts/spike_worker_ws.go` (kept for reference)

## Completion Notes — 2026-04-01

### Phases Implemented
- **Phase 01** ✅ WS endpoint + WorkerSession + NDJSON handler (`internal/http/worker_session.go`, `worker_session_manager.go`)
- **Phase 02** ✅ Worker script stream_mode (`scripts/local-coding-worker.ps1`)
- **Phase 03** ✅ Broadcast stream events via event bus (`WorkerStreamPayload`)
- **Phase 04** ✅ Agent interrupt/inject via `team_tasks` tool actions
- **Phase 05** ⏸️ Dashboard live viewer — deferred (UI not needed for MVP)
- **Phase 06** ✅ Bidirectional agent control (`inject_message`, `SendControlResponse`)

### Critical Protocol Discoveries (Production)
1. Server MUST send prompt after `control_response{success}`, NOT after `system/init`. CLI waits for user message before sending `system/init`.
2. Must use `context.Background()` with tenant_id in post-result goroutine — `r.Context()` is cancelled after WS upgrade.
3. Must send `end_session` control_request after receiving result so Claude CLI exits gracefully.
4. WS auth needs `?token=` query param — Claude CLI doesn't send Authorization header on WS.
5. Nginx needs dedicated location block for `/v1/teams/.*/worker/stream/` with WS upgrade headers.

### Bugs Fixed During Testing
- `fix: send prompt after control_response, not after system/init`
- `fix: use background context for post-stream task update`
- `fix: send end_session after result so Claude CLI exits`
- `fix: propagate tenant_id into background context for post-stream task update`

### E2E Test Results (Task #8)
- Full flow: claim → WS connect → initialize → prompt → streaming (2 turns) → result{success, $0.23, 22s} → auto-submit to `in_review` → review_notify → agent reviewed → approved → done
- Worker script completed cleanly, Claude CLI exited with close 1000 (normal)
