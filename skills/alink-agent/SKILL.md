# AethroLink Agent Integration

Use AethroLink as the local node control plane for registration, heartbeat, and
task delegation.

Do not treat this skill as the protocol. The protocol is the node HTTP API,
wrapped by `alink-cli`. This skill is the workflow guide for using that client
correctly.

## Read First

Before using AethroLink from an agent, read:

- [Agent Integration Reference](./references/agent-integration.md)

## Required Behavior

- Before using AethroLink, check whether `alink-cli` is available on `PATH`.
- Use `alink-cli`, not raw `curl`, when the CLI is available.
- If `alink-cli` is not on `PATH`, install or link it before proceeding.
- Bootstrap first-time registration with `alink-cli register` when you need to set an explicit `agent_id`.
- Use `alink-cli ensure-registered` only after a state file already exists or when you do not need to override the stored identity.
- Reuse the local state file so the node sees a stable `agent_id`.
- Heartbeat before long-running work or before delegating tasks.
- Do not treat heartbeat as a registration or discovery requirement. Registered targets should remain discoverable even when offline so the node can launch them on demand.
- Register OpenClaw-backed targets with `adapter=acp` and `dialect=openclaw` when using the current node build; the composition root registers the generic ACP adapter, not a separate `openclaw` adapter key.
- Discover available targets with:
  - `alink-cli agents`
  - `alink-cli targets`
- Submit tasks through the node with `alink-cli call`.
- Use `conversation-id` when sticky conversational continuity is desired.

## Core Commands

Check availability:

```bash
command -v alink-cli
```

If missing, install or link it:

```bash
go build -o /tmp/aethrolink/alink-cli ./cmd/alink-cli
mkdir -p ~/.local/bin
ln -sf /tmp/aethrolink/alink-cli ~/.local/bin/alink-cli
```

```bash
alink-cli ensure-registered ...
alink-cli heartbeat ...
alink-cli agents ...
alink-cli targets ...
alink-cli call ...
alink-cli task-get ...
alink-cli task-events ...
```
