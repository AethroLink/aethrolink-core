# Adapter Contracts

This document is **normative** for runtime adapter behavior and fake runtime behavior used in tests.

It intentionally does **not** restate architecture or HTTP API schemas.

## 1. Shared Adapter Rules

All runtime adapters must implement the same semantic contract.

### 1.1 `ensure_ready(options)`

Rules:

- must be safe to call multiple times
- must return a `RuntimeLease` when the runtime is usable
- may launch or warm the runtime if needed
- may reuse an existing active lease if appropriate for that adapter
- must not create a remote task
- must not block indefinitely

### 1.2 `submit(task, lease)`

Rules:

- must start remote execution or equivalent task handling
- must return a `RemoteHandle` once the runtime has accepted the work or the adapter has enough remote metadata to track it
- must not wait for final completion
- if the runtime never accepts the task, return an error and let the core mark the task as failed

### 1.3 `stream_events(handle)`

Rules:

- must yield ordered events for one remote execution
- may be backed by polling, SSE, JSON-RPC notifications, or adapter-generated events
- must terminate after terminal remote outcome
- must not emit duplicate terminal events

### 1.4 `resume(handle, payload)`

Rules:

- only valid when the task is in `awaiting_input`
- adapter must return an error if resume is unsupported or invalid
- adapter must not mutate local task state directly; the core owns state transitions

### 1.5 `cancel(handle)`

Rules:

- must be idempotent where possible
- if cancellation is unsupported, return an explicit error
- if remote runtime already completed, adapter may treat cancel as a no-op

### 1.6 `health(options)`

Rules:

- must return adapter-specific health detail
- must not create a remote task
- should perform a lightweight readiness check

## 2. Canonical Adapter Error Kinds

Adapters should normalize internal failures to these categories:

- `not_found`
- `unavailable`
- `launch_failed`
- `submit_failed`
- `protocol_error`
- `resume_not_supported`
- `cancel_not_supported`
- `timeout`
- `remote_failed`

The core may wrap these into API errors and `last_error.reason`.

## 3. Hermes Adapter Contract

Public runtime id:

- registry-defined semantic targets backed by Hermes, for example `core` or `research`

Hermes executors are **not** public runtime ids.

## 3.1 Accepted runtime options

```json
{
  "executor": "coder",
  "cwd": "/workspace/app",
  "workspace": "/workspace/app",
  "model": "default"
}
```

Rules:

- `executor` default comes from registry
- `cwd` is optional
- `workspace` is optional
- unknown runtime options must be ignored unless they affect correctness

## 3.2 Lease model

Hermes uses a sticky subprocess worker pool keyed by executor.

Required behavior:

- `subcontext_key = "executor:<executor-name>"`
- one live worker per executor is sufficient in v0.1
- if an alive worker already exists for the executor, reuse it
- if none exists, spawn using the registry command for that executor

## 3.3 Session model

Keep this simple in v0.1:

- worker continuity is sticky by executor
- prompt/session continuity is **best-effort**
- adapter may create a fresh ACP client session per task by default
- adapter may reuse a session if it already has one for the same worker and workspace
- session reuse is optional in v0.1
- worker reuse is required

## 3.4 Submit behavior

Required flow:

1. ensure worker exists
2. initialize ACP client connection if needed
3. create or load a session
4. submit a prompt/request to the session
5. return a `RemoteHandle`

Recommended `RemoteHandle` fields:

```json
{
  "binding": "acp_client_stdio",
  "remote_execution_id": "session_or_turn_id",
  "remote_session_id": "session_id",
  "adapter_state": {
    "executor": "coder"
  }
}
```

## 3.5 Resume behavior

If the fake or real ACP client flow supports resumable input, map it.
Otherwise:

- return `resume_not_supported`

## 3.6 Cancel behavior

For ACP client flows, cancellation should use session-scoped cancellation if available.

If cancellation succeeds:

- the adapter event stream must end with a cancelled outcome

## 4. OpenClaw Adapter Contract

Public runtime id:

- registry-defined semantic targets backed by OpenClaw, for example `gateway`

Gateway session keys are **not** public runtime ids.

## 4.1 Accepted runtime options

```json
{
  "session_key": "main",
  "cwd": "/workspace/app"
}
```

Rules:

- `session_key` default comes from registry
- session continuity is required for the same `session_key`
- `cwd` is optional and adapter-specific

## 4.2 Lease model

OpenClaw continuity is keyed by `session_key`.

Required behavior:

- `subcontext_key = "session:<session_key>"`
- adapter must persist session continuity metadata
- adapter may reuse one bridge process across multiple session keys
- adapter may also choose one subprocess per session key
- whichever strategy is used must be consistent and persisted

## 4.3 Submit behavior

Required flow:

1. ensure bridge/connection exists
2. resolve or create gateway session for the given `session_key`
3. submit task through ACP client driver
4. return a `RemoteHandle`

Recommended `RemoteHandle` fields:

```json
{
  "binding": "acp_client_stdio",
  "remote_execution_id": "session_key_or_turn_id",
  "remote_session_id": "gateway_session_id",
  "adapter_state": {
    "session_key": "main"
  }
}
```

## 4.4 Continuity rule

For repeated submissions with the same `session_key`:

- adapter must reuse the same persisted gateway/session continuity where possible
- tests must verify this by checking stable metadata across multiple task submissions

## 5. HTTP ACP Runtime Adapter Contract

Adapter kind:

- `acp_comm_http`

Public runtime ids are loaded from registry.

## 5.1 Accepted runtime options

Runtime options are optional and adapter-specific.
v0.1 must support:

```json
{
  "headers": {
    "x-api-key": "test"
  }
}
```

If registry already contains auth headers, runtime options may extend but not replace them unless explicitly designed.

## 5.2 Submit behavior

Required flow:

1. ensure endpoint is healthy or launch if configured
2. create ACP-style run request
3. inject AethroLink control part as first message part
4. return `RemoteHandle` with remote run/session identifiers

Recommended `RemoteHandle` fields:

```json
{
  "binding": "acp_comm_http",
  "remote_execution_id": "run_abc123",
  "remote_session_id": "sess_abc123",
  "adapter_state": {
    "events_url": "/runs/run_abc123/events"
  }
}
```

## 5.3 Event mapping

Map remote lifecycle to local events as follows:

- remote created / accepted -> `task.running`
- remote progress -> adapter event that keeps local state `running`
- remote awaiting -> `task.awaiting_input`
- remote completed -> `task.completed`
- remote failed -> `task.failed`
- remote cancelled -> `task.cancelled`

## 6. Future Stub Adapters

These adapters must exist as compileable stubs only:

- `GatewayNativeAdapter`
- `A2AAdapter`

Rules:

- return `not_implemented` or equivalent errors
- compile cleanly
- do not affect v0.1 runtime behavior

## 7. Driver Contracts

## 7.1 ACP Client Driver

This driver is used by Hermes and OpenClaw adapters.

Support at least these concepts:

- initialize connection
- optional authentication handling
- create new session
- load existing session when available
- submit prompt/request
- cancel session or prompt
- receive notifications/events
- close subprocess cleanly

For tests, the fake ACP client server must support a minimal subset with JSON-RPC over stdio.

### Required fake methods

The fake server must recognize:

- `initialize`
- `session/new`
- `session/load`
- `session/prompt`
- `session/cancel`

The fake server may also emit notifications such as:

- `session/update`

The coding agent does not need to implement the full public ACP client surface for v0.1 tests.
It only needs the minimal subset above.

## 7.2 HTTP ACP Driver

This driver is used by the HTTP ACP runtime adapter.

Support at least:

- create run
- stream or poll events
- fetch status if needed
- resume run
- cancel run

For tests, the fake HTTP server must support:

- `POST /runs`
- `GET /runs/{run_id}`
- `GET /runs/{run_id}/events`
- `POST /runs/{run_id}/resume`
- `POST /runs/{run_id}/cancel`

## 8. Fake Runtime Contracts for Tests

The fake runtimes are mandatory.
They are how adapter tests stay deterministic.

## 8.1 Fake ACP client server modes

Implement these deterministic modes:

### `success`
- accepts prompt
- emits one progress update
- finishes successfully

### `await_then_resume`
- accepts prompt
- emits waiting state
- resumes when `resume` is called
- completes successfully

### `cancelled`
- accepts prompt
- waits
- on cancel returns cancelled outcome

### `submit_fail`
- rejects prompt creation

### `crash_after_start`
- starts successfully
- exits unexpectedly before completion

## 8.2 Fake HTTP ACP server modes

Implement the same logical modes:

- `success`
- `await_then_resume`
- `cancelled`
- `submit_fail`
- `launch_fail` when paired with managed local process mode

## 9. Adapter Output Contract

Adapters should emit normalized internal outcomes to the core.
Do not expose raw protocol frames above the adapter boundary.

Preferred normalized event payload shapes:

### running

```json
{
  "kind": "task.running",
  "message": "Runtime accepted the task",
  "data": {
    "remote_execution_id": "abc123"
  }
}
```

### awaiting_input

```json
{
  "kind": "task.awaiting_input",
  "message": "Runtime requires additional input",
  "data": {
    "prompt": "Approve file write?"
  }
}
```

### completed

```json
{
  "kind": "task.completed",
  "message": "Task completed",
  "data": {
    "result_artifact_id": "01JY..."
  }
}
```

### failed

```json
{
  "kind": "task.failed",
  "message": "Runtime reported failure",
  "data": {
    "reason": "remote_failed"
  }
}
```

## 10. Rules to Avoid Confusion

- runtime adapters own runtime semantics
- drivers own protocol mechanics
- the core owns local task state
- fake runtimes are required, not optional
- do not leak raw JSON-RPC requests or raw HTTP event objects into the core
