# AethroLink

AethroLink is a local-first orchestration layer for agent-to-agent task delegation that is designed to evolve into a decentralized protocol node over time.

The immediate goal of `v0.1` is simple:

- one runtime sends a task to another runtime
- progress is streamed back
- a final response is returned
- the target runtime is started automatically if it is down

The long-term goal is more ambitious:

- keep the same core task model
- add decentralized transport
- add decentralized discovery
- add stronger identity and trust layers
- support multiple protocol bindings without rewriting the core

## Current implementation direction

AethroLink is now a Go-first codebase.

That means:

- new implementation work should target Go
- architecture docs should describe Go packages and binaries
- prior Rust-era implementation ideas should be treated as migration history, not the forward plan
- the core remains runtime-agnostic even though Hermes is the first practical adapter target

The point of this change is not language aesthetics. The point is a lighter operational model, faster iteration, and simpler deployment while the product is still proving its first durable execution path.

## Why AethroLink exists

Today, agent systems are fragmented across different runtime models and protocol surfaces.

Some runtimes expose HTTP APIs.
Some expose local stdio or JSON-RPC flows.
Some are gateway-backed.
Some are editor-native.
Some will eventually speak A2A or other interoperability protocols.

AethroLink exists to provide a stable orchestration core above those runtime differences.

## Design philosophy

AethroLink is built around four architectural ideas:

1. Runtime-first, not protocol-first
   - The core routes to runtimes.
   - Protocols are implementation details inside adapters.

2. Task lifecycle belongs to AethroLink
   - Local `task_id` is always primary.
   - Remote protocol identifiers are secondary bindings.

3. Transport is separate from execution
   - Runtime execution and network delivery are separate concerns.
   - This allows the local MVP to remain clean while future decentralized transports are added later.

4. Decentralized evolution is a present constraint
   - Discovery, transport, identity, and storage must be isolated from day one.
   - The local MVP should not lock the system into a centralized-only design.

## What v0.1 includes

- local control API
- runtime routing by intent/capability
- runtime adapters for:
  - Hermes
  - OpenClaw
  - HTTP ACP-style runtimes
- local loopback transport
- static registry-based discovery
- local identity abstraction
- SQLite task/event persistence
- artifact storage on disk
- runtime supervision and launch-if-down behavior
- SSE task event streaming

## What v0.1 does not include

- blockchain anchoring
- libp2p or Waku networking
- on-chain discovery
- DIDs or wallet-based identity
- verifiable credentials
- public peer discovery
- dynamic plugin loading

## Public model

AethroLink exposes runtimes as execution targets.

Examples:

- `core`
- `research`
- `gateway`

AethroLink does not expose:

- raw protocol endpoints as public runtime IDs
- arbitrary per-request Hermes executor names outside registry-defined runtimes
- arbitrary per-request OpenClaw session keys outside registry-defined runtimes
- protocols as first-class public targets

Executors and session keys belong in `runtime_options`.

## Key architecture idea

The core system should look like this:

- AethroLink Core owns task lifecycle
- Runtime adapters translate tasks into runtime-specific operations
- Transport adapters handle node-to-node delivery
- Discovery providers resolve runtimes
- Identity providers sign and verify node-level envelopes

Only the first slice is fully implemented in `v0.1`, but all boundaries should exist now.

## Runtime adapters

### Hermes
- Runtime IDs should be stable semantic targets from registry, not Hermes-shaped names
- Hermes executors remain adapter-private execution scopes behind those targets
- `runtime_options.executor` may override the internal execution scope when needed
- No legacy `runtime_options.profile` fallback remains

### OpenClaw
- OpenClaw-backed runtime IDs should also be semantic registry targets such as `gateway`
- `runtime_options.session_key` selects the internal continuity context
- Session keys are adapter-private, not public runtime IDs

### ACP communication HTTP runtimes
- Runtime IDs come from registry
- Local `task_id` remains primary
- Remote `run_id` and `session_id` are secondary bindings

## State model

AethroLink owns its own local task state machine:

```text
created
-> pending_launch
-> launching
-> ready
-> dispatching
-> running
-> awaiting_input
-> completed | failed | cancelled
```

This is important because AethroLink must track work even before a target runtime has accepted a remote run.

## Repository shape

Recommended Go-first layout:

```text
aethrolink-core/
  go.mod
  cmd/
    alink-node/
    alink-cli/
    fake-acp-client-agent/
    fake-acp-comm-agent/
  internal/
    api/
    core/
    adapters/
    drivers/
    runtime/
    storage/
    transport/
    config/
  pkg/
    types/
    contracts/
  configs/
    registry.yaml
  docs/
    overview.md
    contracts.md
    state_machine.md
    adapter_contracts.md
    implementation_plan.md
  tests/
    integration/
```

## Recommended stack

- Go
- chi or stdlib `net/http`
- `context` for lifecycle management
- `database/sql` with SQLite driver
- JSON/YAML config parsing
- SSE over HTTP
- structured logging with `slog`
- Cobra only if CLI complexity justifies it

## Implementation priority

1. Build the core types and interfaces
2. Implement SQLite-backed task/event persistence
3. Implement static registry discovery
4. Implement local loopback transport
5. Implement Hermes adapter
6. Implement OpenClaw adapter
7. Implement ACP communication HTTP adapter
8. Expose HTTP API and SSE
9. Add integration tests with fake runtimes

## Documentation

- See `docs/overview.md` for the full implementation architecture.
- See `docs/implementation_plan.md` for the build order.
- See `docs/contracts.md` and `docs/adapter_contracts.md` for normative contracts.

## Local Agent CLI

Local agents should prefer `alink-cli` over handcrafting raw HTTP calls.

To make `alink-cli` available on `PATH` for local agent runtimes:

```bash
go build -o /tmp/alink-cli ./cmd/alink-cli
mkdir -p ~/.local/bin
ln -sf /tmp/alink-cli ~/.local/bin/alink-cli
```

Verify:

```bash
command -v alink-cli
```

Thread continuity inspection example:

```bash
alink-cli thread-get --server http://127.0.0.1:7777 --thread-id <thread-id>
```

That inspection view shows:
- persisted thread state
- ordered turns
- current reusable session bindings per agent
- the next sender/target pair and whether the next continue call should reuse a remote session
- interruption reason when restart reconciliation marked the thread interrupted

## Status

This repository is intended to start as a local-first runtime orchestration node and grow into a protocol node later without replacing the core.
