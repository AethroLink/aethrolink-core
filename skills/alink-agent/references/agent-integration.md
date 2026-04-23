# Agent Integration Reference

This reference defines how a local agent should use AethroLink through
`alink-cli`.

## 1. Mental Model

AethroLink is the **node control plane**.

The agent should:

1. register itself
2. keep its lease alive with heartbeat
3. discover available agents/targets
4. submit tasks through the node
5. retrieve state and stream events through the node

The CLI is the preferred integration surface.

## 1.1 CLI Availability

Before using AethroLink from an agent, confirm that `alink-cli` is available on
`PATH`:

```bash
command -v alink-cli
```

If it is missing, install or link it first. One simple local workflow is:

```bash
go build -o /tmp/alink-cli ./cmd/alink-cli
mkdir -p ~/.local/bin
ln -sf /tmp/alink-cli ~/.local/bin/alink-cli
```

After that, verify:

```bash
command -v alink-cli
```

Agents should prefer the PATH-installed CLI over raw HTTP calls.

## 2. Key Terms

### Agent

A registered executable participant known to the node.

Examples:

- Hermes profile
- Gemini ACP runtime
- OpenClaw bridge-backed runtime

An agent record includes:

- `agent_id`
- `display_name`
- `transport_kind`
- `adapter`
- `dialect`
- `launch`
- `defaults`
- `capabilities`
- `sticky_mode`

### Target

The node-facing execution target used for task routing.

In the current architecture, a runtime is derived from a registered agent
record. The registered agent's `agent_id` is the direct machine-facing
identifier used when submitting tasks.

Examples:

- `core`
- `research`
- `gateway`
- `gemini`

### Intent

The **caller-side description of the work requested**.

Intent is how the submitter tells the node what kind of task it wants done.
It should be short, stable, and capability-aligned.

Current routing is based on **exact capability match** against registered
runtimes/agents.

Examples:

- `agent.runtime`
- `code.patch`
- `code.review`
- `summarize`
- `ui.review`

### Capability

The **callee-side claim** about what work an agent/runtime can handle.

Capabilities are attached during registration. The safest rule is:

> if you want a runtime to receive tasks for intent `X`, register capability `X`

So for now, intents and capabilities should usually share the same string.

### Sticky Session / Continuity

A hint that the caller wants the callee/runtime to continue prior context if the
runtime supports it.

Current practical hint:

- `conversation_id`

Future runtime-specific hints may include:

- `session_key`
- `sticky_key`

## 3. Capability Guidance

There is no hardcoded finite capability registry in the node today.

However, capabilities should follow these rules:

- use lowercase strings
- prefer dot-delimited namespaces
- keep them stable
- register only what the runtime can actually do
- make intents and capabilities identical where possible

Recommended examples:

- `agent.runtime`
- `code.patch`
- `code.review`
- `summarize`
- `research.topic`
- `ui.review`
- `gateway.chat`
- `thread.reply`

Bad examples:

- `do_stuff`
- `misc`
- `help`
- long sentence-like strings

## 4. Discovering What Exists

An agent should not assume which targets exist.

Use:

```bash
alink-cli agents --server http://127.0.0.1:7777
alink-cli targets --server http://127.0.0.1:7777
```

Use `agents` when you want full registered-agent records.

Use `targets` when you want the node-facing execution targets available for
task routing. Today those target identifiers are the registered agents'
`agent_id` values.

## 5. Registration Workflow

### Bootstrap first-time registration

Use `register` for the first bootstrap when you need to set an explicit `agent_id`.
The current CLI implementation of `ensure-registered` does not accept `--agent-id`; it reuses the ID from the state file if one already exists.

```bash
alink-cli register \
  --server http://127.0.0.1:7777 \
  --state-file ~/.aethrolink/agent.json \
  --agent-id core \
  --display-name aethrolink-agent \
  --transport-kind local_managed \
  --adapter acp \
  --dialect hermes \
  --launch-mode managed \
  --launch-command "hermes -p aethrolink-agent acp" \
  --defaults executor=aethrolink-agent \
  --capabilities agent.runtime,code.patch,code.review \
  --sticky-mode conversation
```

### Ensure registration on startup

After the state file exists, use:

```bash
alink-cli ensure-registered \
  --server http://127.0.0.1:7777 \
  --state-file ~/.aethrolink/agent.json \
  --display-name aethrolink-agent \
  --transport-kind local_managed \
  --adapter acp \
  --dialect hermes \
  --launch-mode managed \
  --launch-command "hermes -p aethrolink-agent acp" \
  --defaults executor=aethrolink-agent \
  --capabilities agent.runtime,code.patch,code.review \
  --sticky-mode conversation
```

### Why these fields matter

- `adapter`:
  which node adapter should execute it
  - on the current Go node, use `acp` for both Hermes and OpenClaw ACP-backed targets
  - do not register OpenClaw with `adapter=openclaw` unless the node composition root actually registers an adapter under that key
- `dialect`:
  runtime-specific ACP behavior
  - use `hermes` for Hermes-backed targets
  - use `openclaw` for OpenClaw-backed targets
- `launch-mode` + `launch-command`:
  how the node starts the runtime
- `defaults`:
  runtime options applied automatically
- `capabilities`:
  what intents this target can accept

### Discovery and heartbeat semantics

- Registration creates a durable target entry.
- Heartbeat refreshes liveness; it should not be required for the target to stay visible in discovery.
- Offline registered targets should still appear in `alink-cli targets` so the orchestrator can launch them when a task is dispatched.
- If a target disappears from discovery when its lease expires, that is a node bug, not expected workflow.

## 6. Heartbeat Workflow

Refresh the lease regularly:

```bash
alink-cli heartbeat --server http://127.0.0.1:7777
```

Recommended times to heartbeat:

- before long work
- before submitting delegated tasks
- on a periodic loop for long-lived agents

## 7. Invocation Workflow

Submit work through the node:

```bash
alink-cli call \
  --server http://127.0.0.1:7777 \
  --target-agent-id research \
  --intent summarize \
  --text "Summarize this result" \
  --conversation-id "$HERMES_CONVERSATION_ID" \
  --heartbeat
```

What happens:

- `sender` comes from the stored `agent_id`
- `target-agent-id` selects the destination target
- `intent` states the requested work category
- `conversation-id` hints sticky reuse when supported
- `--heartbeat` refreshes the sender lease before submission

## 8. Retrieval Workflow

### Fetch task state

```bash
alink-cli task-get \
  --server http://127.0.0.1:7777 \
  --task-id <task-id>
```

### Stream task events

```bash
alink-cli task-events \
  --server http://127.0.0.1:7777 \
  --task-id <task-id>
```

Use `task-events` when waiting on a live task.
Use `task-get` when you only need the latest state/result.

## 9. State File

Default path:

```text
~/.aethrolink/agent.json
```

Purpose:

- stores the stable `agent_id`
- lets `ensure-registered`, `heartbeat`, and `call` operate without repeating it

Override with:

```bash
--state-file /path/to/file.json
```

## 10. Hermes-style Example

First bootstrap with an explicit stable ID:

```bash
alink-cli register \
  --server http://127.0.0.1:7777 \
  --state-file ~/.aethrolink/agent.json \
  --agent-id core \
  --display-name aethrolink-agent \
  --transport-kind local_managed \
  --adapter acp \
  --dialect hermes \
  --launch-mode managed \
  --launch-command "hermes -p aethrolink-agent acp" \
  --defaults executor=aethrolink-agent \
  --capabilities agent.runtime,code.patch,code.review \
  --sticky-mode conversation
```

Later restarts can reuse the state file:

```bash
alink-cli ensure-registered \
  --server http://127.0.0.1:7777 \
  --state-file ~/.aethrolink/agent.json \
  --display-name aethrolink-agent \
  --transport-kind local_managed \
  --adapter acp \
  --dialect hermes \
  --launch-mode managed \
  --launch-command "hermes -p aethrolink-agent acp" \
  --defaults executor=aethrolink-agent \
  --capabilities agent.runtime,code.patch,code.review \
  --sticky-mode conversation
```

Delegation:

```bash
alink-cli call \
  --target-agent-id research \
  --intent summarize \
  --text "Summarize the latest output" \
  --conversation-id "$HERMES_CONVERSATION_ID" \
  --heartbeat
```

## 11. OpenClaw Registration Rule

Do not assume bare `openclaw acp` is the right launch command.

Use this sequence first:

1. inspect the existing gateway
2. if the default gateway is healthy, point ACP at it explicitly
3. only invent a different gateway port if the default gateway is actually broken or unavailable

Useful checks:

```bash
openclaw gateway status
openclaw gateway probe
openclaw gateway health
```

On this machine, the validated path was to reuse the healthy default gateway on `ws://127.0.0.1:18789` explicitly.

Validated OpenClaw registration:

```bash
alink-cli register \
  --server http://127.0.0.1:7777 \
  --state-file ~/.aethrolink/openclaw-main.json \
  --agent-id openclaw_main \
  --display-name openclaw-main \
  --transport-kind local_managed \
  --adapter acp \
  --dialect openclaw \
  --launch-mode managed \
  --launch-command "openclaw acp --url ws://127.0.0.1:18789 --session agent:main:main" \
  --defaults session_key=main \
  --capabilities agent.runtime,code.patch,code.review \
  --sticky-mode conversation
```

Reason:

- explicit `--url` avoids ambiguous default-gateway resolution inside managed launches
- explicit `--session agent:main:main` pins the known session scope
- keep machine-specific environment overrides out of shared repo guidance

Symptom to watch for:

- if task submit fails with messages like `Kill anything bound to the default gateway port, then start it`, inspect the default gateway first before changing ports

## 12. HTTP Endpoints Behind the CLI

Registration:

- `POST /v1/agents/register`
- `POST /v1/agents/{agent_id}/heartbeat`
- `GET /v1/agents`
- `GET /v1/agents/{agent_id}`
- `POST /v1/agents/{agent_id}/unregister`

Invocation:

- `POST /v1/tasks`
- `GET /v1/tasks/{task_id}`
- `GET /v1/tasks/{task_id}/events`
- `POST /v1/tasks/{task_id}/resume`
- `POST /v1/tasks/{task_id}/cancel`
