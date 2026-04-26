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

### CLI version mismatch

If a command such as `peer-add`, `peer-list`, `peer-sync`, or `targets --refresh`
returns `unknown command` or `flag provided but not defined`, the agent is likely
calling an older `alink-cli` on `PATH`.

Check both the PATH binary and the freshly built repo binary:

```bash
command -v alink-cli
alink-cli 2>&1 | head -n 1
/tmp/aethrolink-phase8/alink-cli 2>&1 | head -n 1
```

Repair the PATH binary from the current repo before continuing:

```bash
cd /Users/clawuser/Desktop/AETH/aethrolink-core
go build -o ~/.local/bin/alink-cli ./cmd/alink-cli
```

Do not debug peer workflow behavior until the CLI help output includes
`peer-add`, `peer-list`, `peer-sync`, and `targets --refresh`.

## 2. Key Terms

### Node

An AethroLink server process, usually started with `alink-node`.

For local two-node testing:

- Node A is usually `http://127.0.0.1:7777`
- Node B is usually `http://127.0.0.1:7778`

Each node has its own SQLite database, artifact directory, registered local
agents, and cached view of remote peer targets.

### Peer

A peer is another AethroLink node registered in the local node's static-peer
registry.

Example: when Node A registers Node B as `node-b`, Node B becomes a peer from
Node A's point of view.

```bash
alink-cli peer-add \
  --server http://127.0.0.1:7777 \
  --peer-id node-b \
  --base-url http://127.0.0.1:7778 \
  --display-name "Node B"
```

`peer_id` is the local name for that remote node. It is not an agent id.

The controller of the origin node adds peers. In practice, that can be a human,
this outer Hermes/aethrolink-agent session, a dashboard/backend service, or later
authorized automation. It is not Node B registering itself, the remote agent
registering itself with Node A, or Hermes/OpenClaw adapter logic.

Peer registration is one-way. If Node A adds Node B, only Node A's `peers` table
changes. Node B does not automatically learn about Node A. To make Node B know
Node A, add the reverse peer explicitly:

```bash
alink-cli peer-add \
  --server http://127.0.0.1:7778 \
  --peer-id node-a \
  --base-url http://127.0.0.1:7777 \
  --display-name "Node A"
```

### Agent

A registered executable participant known to a node.

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

### Local agent

A local agent is registered on the node you are querying. The node can launch or
reuse this runtime directly through its local adapter stack.

Example: if `aethrolink-agent` is registered on Node A, then Node A shows it as:

```json
{ "target_id": "aethrolink-agent", "owner": "local" }
```

### Remote agent / peer-owned target

A remote agent is not launched by the current node. It is a target exported by a
peer node and cached locally as a `peer_target`.

Example: if `videographer` is registered on Node B and Node A syncs `node-b`,
then Node A shows it as:

```json
{
  "target_id": "videographer",
  "owner": "remote",
  "peer_id": "node-b",
  "peer_base_url": "http://127.0.0.1:7778"
}
```

When Node A submits work to this remote target, Node A creates the
operator-facing proxy task, relays the task to Node B, and Node B executes it
through its own local adapter. Runtime adapters remain local-only.

### Target

The node-facing execution target used for task routing.

A target can be:

- local: directly registered on this node
- remote: cached from a peer node after peer sync or refresh

In the current architecture, local runtime targets are derived from registered
agent records. Remote targets are derived from cached `peer_targets` records.

Examples:

- `core`
- `research`
- `gateway`
- `gemini`
- `videographer`
- `openclaw_main`

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
alink-cli targets --server http://127.0.0.1:7777 --refresh
```

Use `agents` when you want full registered-agent records.

Use plain `targets` when you want the node-facing execution targets available for
task routing from the local cache. Use `targets --refresh` when you need the node
to synchronously refresh all registered peer caches first.

To list static peers registered on this node, use:

```bash
alink-cli peer-list --server http://127.0.0.1:7777
```

This calls `GET /v1/peers` and returns this node's local peer registry. It does
not probe peer liveness or fetch peer targets.

To refresh exactly one static peer cache, use:

```bash
alink-cli peer-sync --server http://127.0.0.1:7777 --peer-id node-b
```

The peer sync path calls that peer's `/v1/node/health` and `/v1/targets`, then
updates the origin node's cached `peer_targets`.

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

### Heartbeat scope

Heartbeat is only for the **sender agent** registered locally on the node you are calling.
It is not target validation, and it is not remote-target discovery.

Bad cross-node call from Node B:

```bash
alink-cli call \
  --server http://127.0.0.1:7778 \
  --target-agent-id aethrolink-agent \
  --intent agent.runtime \
  --text "Reply exactly OPENCLAW_SIMPLE_OK" \
  --heartbeat
```

Why this fails:

- without `--agent-id`, `call` reads the default state file for the sender id
- if that state file contains `aethrolink-agent`, the CLI heartbeats `aethrolink-agent` on Node B
- Node B only has `aethrolink-agent` as a remote cached target, not a local agent
- `/v1/agents/aethrolink-agent/heartbeat` checks Node B's local agent table and returns `agent not found`

Good cross-node call from Node B:

```bash
alink-cli call \
  --server http://127.0.0.1:7778 \
  --agent-id openclaw_main \
  --target-agent-id aethrolink-agent \
  --intent agent.runtime \
  --text "Reply exactly OPENCLAW_SIMPLE_OK" \
  --heartbeat
```

Rule: on cross-node calls, `--server` selects the origin node, `--agent-id` names a local sender on that origin node, and `--target-agent-id` may name a local or remote target.

## 7. Invocation Workflow

Submit local-to-local work through the node:
```bash
alink-cli call \
  --server http://127.0.0.1:7777 \
  --target-agent-id research \
  --intent summarize \
  --text "Summarize this result" \
  --conversation-id "$HERMES_CONVERSATION_ID" \
  --heartbeat
```

Submit from a Node B local agent to a Node A remote target:
```bash
alink-cli call \
  --server http://127.0.0.1:7778 \
  --agent-id openclaw_main \
  --target-agent-id aethrolink-agent \
  --intent agent.runtime \
  --text "Reply exactly OPENCLAW_SIMPLE_OK" \
  --heartbeat
```

Do not omit `--agent-id` on cross-node calls when the default state file belongs
to a different node. Otherwise `call --heartbeat` may try to heartbeat a remote
peer-owned target on the local node and fail with `agent not found` before task
submission. The known-bad shape is:

```bash
alink-cli call --server http://127.0.0.1:7778 --target-agent-id aethrolink-agent --intent agent.runtime --text "..." --heartbeat
```

What happens:

- `sender` comes from `--agent-id` when provided, otherwise from the stored state file
- `target-agent-id` selects the destination target, which may be local or remote
- `intent` states the requested work category
- `conversation-id` hints sticky reuse when supported
- `--heartbeat` refreshes the sender lease before submission and only works for local agents on the node being called

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
