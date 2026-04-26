# AethroLink Agent Integration

Use AethroLink as the local node control plane for registration, heartbeat, and
task delegation.

Do not treat this skill as the protocol. The protocol is the node HTTP API,
wrapped by `alink-cli`. This skill is the workflow guide for using that client
correctly.

## Read First

Before using AethroLink from an agent, read:

- [Agent Integration Reference](./references/agent-integration.md)

When AethroLink workflow commands change, update this repo-visible skill and its
`references/` files. Do not treat profile-local Hermes skills as a substitute
for the repo-local agent integration contract.

## Required Behavior

- Before using AethroLink, check whether `alink-cli` is available on `PATH`.
- Use `alink-cli`, not raw `curl`, when the CLI is available.
- If `alink-cli` is not on `PATH`, install or link it before proceeding.
- If a peer command returns `unknown command`, verify `command -v alink-cli` and rebuild the PATH binary from this repo; stale `~/.local/bin/alink-cli` may not include peer commands.
- Bootstrap first-time registration with `alink-cli register` when you need to set an explicit `agent_id`.
- Use `alink-cli ensure-registered` only after a state file already exists or when you do not need to override the stored identity.
- Reuse the local state file so the node sees a stable `agent_id`.
- Heartbeat before long-running work or before delegating tasks, but only for the local sender agent registered on the node you are calling.
- Never use `--heartbeat` on a cross-node `call` unless `--agent-id` names a local sender on that same `--server` node.
- Do not heartbeat a remote peer-owned target; `/v1/agents/{agent_id}/heartbeat` is local-agent-only and will return `agent not found` for remote targets.
- Register OpenClaw-backed targets with `adapter=acp` and `dialect=openclaw` when using the current node build; the composition root registers the generic ACP adapter, not a separate `openclaw` adapter key.
- Discover available targets with:
  - `alink-cli agents`
  - `alink-cli targets`
  - `alink-cli targets --refresh` when you need fresh peer-owned targets before routing
- List registered static peers with `alink-cli peer-list --server http://127.0.0.1:7777`.
- Refresh one static peer explicitly with `alink-cli peer-sync --server http://127.0.0.1:7777 --peer-id node-b`.
- Ownership model:
  - `owner=local` means this node owns and executes the runtime directly.
  - `owner=remote` means this node only has a cached peer-owned target; execution is relayed to the peer node.
  - `peer_id` names the remote node, not the remote agent.
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
