# Implementation Plan

This document is the build order for `v0.1`.

It is intentionally procedural.
It should be used after reading `overview.md`, `contracts.md`, `state_machine.md`, and `adapter_contracts.md`.

## 1. Guiding rule

Build the repository in small compilable slices.

At the end of every phase:

- `gofmt` should leave the tree clean
- `go test ./...` should pass for the implemented slice
- the module should remain runnable

Do not start with adapters first.
Do not start with the HTTP API first.
Start with the core types and constraints.

## 2. Phase order

## Phase 0 — Module bootstrap

Create the Go module and empty package skeletons:

- `cmd/alink-node`
- `cmd/alink-cli`
- `internal/core`
- `internal/storage`
- `internal/runtime`
- `internal/drivers`
- `internal/adapters`
- `internal/transport`
- `internal/api`
- `internal/config`
- `pkg/types`

Exit criteria:

- module builds
- package dependencies are wired
- formatting and basic linting are set up

## Phase 1 — Shared types and interfaces

Implement in `pkg/types`:

- ids and timestamp helpers
- `TaskStatus`
- `TaskEventKind`
- `EventSource`
- `DeliveryPolicy`
- `TraceContext`
- `TaskCreateRequest`
- `TaskEnvelope`
- `TaskRecord`
- `TaskEvent`
- `RuntimeLease`
- `RemoteHandle`
- `ArtifactRef`
- `NetworkEnvelope`
- `RuntimeSpec`

Implement interfaces:

- `RuntimeAdapter`
- `TransportAdapter`
- `DiscoveryProvider`
- `IdentityProvider`

Exit criteria:

- all shared types compile
- JSON serialization works
- unit tests cover basic encoding and decoding

## Phase 2 — Storage

Implement in `internal/storage`:

- SQLite connection setup
- migrations
- repositories for:
  - tasks
  - task_events
  - runtime_leases
  - artifacts
  - launch_history
  - runtimes
- artifact filesystem storage

Exit criteria:

- migrations run from a blank database
- create/read/update task works
- append-only event writes work
- artifact write/read works

## Phase 3 — State machine

Implement in `internal/core`:

- state transition validator
- event appender
- helper functions:
  - create task
  - transition task
  - fail task
  - complete task
  - cancel task

Exit criteria:

- invalid transitions are rejected
- valid transitions append events and update task state
- state machine tests cover all major transitions

## Phase 4 — Registry and discovery

Implement in:

- `pkg/types` for `RuntimeSpec`
- `internal/core` for route resolution
- `internal/config` for registry loading
- `internal/transport` or `internal/core` for `StaticRegistryDiscovery`

Required features:

- parse `registry.yaml`
- list runtimes
- resolve by runtime id
- resolve route by intent when target runtime is omitted

Exit criteria:

- example registry loads
- routing by explicit runtime works
- routing by intent works
- ambiguous route returns error

## Phase 5 — Local transport and identity

Implement in `internal/transport`:

- `LocalLoopbackTransport`
- `LocalNodeIdentity`

The orchestrator should already use these interfaces even though transport is local-only.

Exit criteria:

- a `NetworkEnvelope` can be published and subscribed locally
- identity provider returns a stable local node id
- no runtime adapter depends directly on transport internals

## Phase 6 — Supervisor and lease pool

Implement in `internal/runtime`:

- subprocess launcher
- managed process lifecycle
- health polling helpers
- lease pool keyed by `runtime_id + subcontext_key`

Exit criteria:

- start process
- detect ready/not ready
- stop process
- reuse existing lease
- persist lease metadata

## Phase 7 — Drivers

Implement in `internal/drivers`.

### 7.1 ACP client stdio driver

Support:

- spawn subprocess
- JSON-RPC request/response
- notifications
- initialize
- session/new
- session/load
- session/prompt
- session/cancel

### 7.2 HTTP ACP driver

Support:

- `POST /runs`
- `GET /runs/{id}`
- `GET /runs/{id}/events`
- `POST /runs/{id}/resume`
- `POST /runs/{id}/cancel`

Exit criteria:

- drivers compile independently of adapters
- fake runtimes can be driven by tests

## Phase 8 — Fake runtimes

Before implementing real adapters, build fake runtimes under `cmd/` or test support packages.

Required fakes:

- fake ACP client stdio server
- fake HTTP ACP server

Required behaviors:

- success
- await_then_resume
- cancelled
- submit_fail
- crash_after_start

Exit criteria:

- driver tests pass against the fakes
- no real Hermes/OpenClaw binary is needed

## Phase 9 — Runtime adapters

Implement in `internal/adapters`:

1. `HermesAdapter`
2. `OpenClawAdapter`
3. `AcpCommHTTPAdapter`
4. `GatewayNativeAdapter` stub
5. `A2AAdapter` stub

Important order:

- Hermes first
- OpenClaw second
- HTTP ACP third

That order is intentional because Hermes and OpenClaw share the ACP client driver.

Exit criteria:

- `EnsureReady` works
- `Submit` works
- `StreamEvents` works
- `Resume` and `Cancel` work where supported
- adapters pass tests against fake runtimes

## Phase 10 — Orchestrator

Implement in `internal/core`:

- task creation flow
- runtime resolution
- readiness flow
- submit flow
- event consumption loop
- cancel path
- resume path
- timeout handling
- task history reconstruction

Exit criteria:

- one task can run end-to-end with a fake runtime
- launch-if-down works
- task events are persisted in order
- terminal states are correct

## Phase 11 — HTTP API and SSE

Implement in `internal/api` and wire through `cmd/alink-node`:

- `POST /v1/tasks`
- `GET /v1/tasks/{task_id}`
- `GET /v1/tasks/{task_id}/events`
- `POST /v1/tasks/{task_id}/resume`
- `POST /v1/tasks/{task_id}/cancel`
- `GET /v1/runtimes`
- `GET /v1/runtimes/{runtime_id}/health`
- `POST /v1/runtimes/{runtime_id}/start`
- `POST /v1/runtimes/{runtime_id}/stop`

Exit criteria:

- HTTP server runs
- SSE streams historical and live events in order
- API returns contract-compliant payloads

## Phase 12 — CLI and polish

Implement in `cmd/alink-cli`:

- submit task
- inspect task
- tail events
- list runtimes

This phase is lower priority than server correctness.

Exit criteria:

- CLI can exercise the main flows locally

## Phase 13 — Integration test sweep

Add full integration tests covering:

- Hermes profile selection via `runtime_options.profile`
- OpenClaw continuity via `runtime_options.session_key`
- HTTP ACP run lifecycle
- launch-if-down
- resume
- cancel
- restart preserving history
- SSE ordering
- routing by intent without explicit target runtime

Exit criteria:

- integration tests pass from a clean environment
- tests do not require real Hermes or OpenClaw

## 3. Suggested package ownership by phase

| Phase | Primary packages |
|---|---|
| 0 | `cmd/*`, `internal/*`, `pkg/types` |
| 1 | `pkg/types` |
| 2 | `internal/storage` |
| 3 | `internal/core` |
| 4 | `pkg/types`, `internal/core`, `internal/config` |
| 5 | `internal/transport` |
| 6 | `internal/runtime` |
| 7 | `internal/drivers` |
| 8 | `cmd/*`, `tests/integration` |
| 9 | `internal/adapters` |
| 10 | `internal/core` |
| 11 | `internal/api`, `cmd/alink-node` |
| 12 | `cmd/alink-cli` |
| 13 | `tests/integration` |

## 4. Acceptance gates

The coding agent should not claim `v0.1` is done unless all of these are true:

1. `go test ./...` passes
2. the HTTP server starts successfully
3. tasks can be submitted to:
   - Hermes adapter
   - OpenClaw adapter
   - HTTP ACP adapter
4. SQLite migrations run cleanly
5. event streaming works over SSE
6. history survives restart
7. fake runtimes are included in the repo
8. stub adapters compile cleanly
9. docs match the implemented contracts

## 5. What not to do

- do not begin by building every adapter fully in parallel
- do not skip fake runtimes
- do not build the HTTP API before the state machine and storage
- do not hide major logic inside one oversized package
- do not make the orchestrator depend on raw protocol-specific frames
- do not add speculative decentralized networking yet

## 6. Final build checklist

Before considering the implementation ready, confirm:

- module tree matches the planned layout
- contracts are implemented as documented
- state transitions are enforced as documented
- adapters follow their contract files
- overview and README still describe the code accurately
