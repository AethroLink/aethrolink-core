# Contracts

This document is **normative** for v0.1 wire formats, API payloads, core records, and storage shape.

It intentionally does **not** restate the architecture from `overview.md`.

## 1. Identifier Rules

Use these identifier formats in v0.1:

- `task_id`: ULID string
- `conversation_id`: ULID string
- `event_id`: ULID string
- `lease_id`: ULID string
- `artifact_id`: ULID string
- `runtime_id`: lowercase slug string
- remote identifiers: opaque strings

Examples:

- `01JY0R4R2V2QKBR8Q2YQ9BMS1V`
- `core`
- `research`

## 2. Canonical Enums

### 2.1 TaskStatus

```text
created
pending_launch
launching
ready
dispatching
running
awaiting_input
completed
failed
cancelled
```

### 2.2 TaskEventKind

```text
task.created
task.routed
runtime.pending_launch
runtime.launching
runtime.ready
task.dispatching
task.running
task.awaiting_input
task.resume_requested
task.resumed
task.cancel_requested
task.completed
task.failed
task.cancelled
artifact.created
runtime.health
```

### 2.3 EventSource

```text
core
adapter
runtime
transport
storage
api
```

## 3. Core Struct Contracts

These types must exist in code.

### 3.1 TaskCreateRequest

```json
{
  "sender": "local",
  "target_runtime": "core",
  "intent": "code.patch",
  "payload": {
    "repo": "/workspace/app",
    "issue": "Fix billing test failures"
  },
  "runtime_options": {
    "executor": "coder"
  },
  "conversation_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "delivery": {
    "mode": "stream",
    "launch_if_down": true,
    "timeout_ms": 60000
  },
  "metadata": {
    "requested_by": "planner"
  }
}
```

Rules:

- `intent` is required
- `payload` is required and may be any JSON value
- `target_runtime` is optional
- if `target_runtime` is omitted, the router must resolve from `intent`
- `runtime_options` defaults to `{}`
- `sender` defaults to `"local"`
- `conversation_id` is generated if absent
- `delivery.launch_if_down` defaults to `true`
- `delivery.mode` defaults to `"stream"`

### 3.2 DeliveryPolicy

```json
{
  "mode": "stream",
  "launch_if_down": true,
  "timeout_ms": 60000
}
```

Rules:

- `mode` is either `stream` or `sync`
- `sync` is best-effort only in v0.1 and may internally still use async processing
- `timeout_ms` is optional
- if omitted, use runtime default or system default

### 3.3 TaskEnvelope

```json
{
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "conversation_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1W",
  "sender": "local",
  "target_runtime": "core",
  "intent": "code.patch",
  "payload": {
    "repo": "/workspace/app",
    "issue": "Fix billing test failures"
  },
  "runtime_options": {
    "executor": "coder"
  },
  "delivery": {
    "mode": "stream",
    "launch_if_down": true,
    "timeout_ms": 60000
  },
  "trace": {
    "trace_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1X",
    "parent_task_id": null
  },
  "metadata": {
    "requested_by": "planner"
  }
}
```

### 3.4 RuntimeLease

```json
{
  "lease_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1Y",
  "runtime_id": "hermes",
  "subcontext_key": "executor:coder",
  "process_id": "12345",
  "metadata": {
    "executor": "coder"
  },
  "created_at": "2026-04-11T10:00:00Z",
  "released_at": null
}
```

Rules:

- `subcontext_key` is adapter-private but persisted
- for Hermes it is typically `executor:<name>`
- for OpenClaw it is typically `session:<key>`
- `process_id` may be null for remote-only runtimes

### 3.5 RemoteHandle

```json
{
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "runtime_id": "researcher_http",
  "binding": "acp_comm_http",
  "remote_execution_id": "run_abc123",
  "remote_session_id": "sess_abc123",
  "adapter_state": {
    "events_url": "http://127.0.0.1:9102/runs/run_abc123/events"
  }
}
```

Rules:

- `remote_execution_id` is opaque
- for HTTP ACP runtimes it should be the remote `run_id`
- for ACP client runtimes it may be a session id, prompt turn id, or adapter-generated opaque ID
- `adapter_state` must be JSON-serializable

### 3.6 TaskRecord

```json
{
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "conversation_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1W",
  "sender": "local",
  "intent": "code.patch",
  "requested_runtime": "core",
  "resolved_runtime": "core",
  "runtime_options": {
    "executor": "coder"
  },
  "status": "running",
  "remote": {
    "binding": "acp_client_stdio",
    "remote_execution_id": "session_123",
    "remote_session_id": "session_123"
  },
  "last_error": null,
  "result_artifact_id": null,
  "created_at": "2026-04-11T10:00:00Z",
  "updated_at": "2026-04-11T10:00:03Z"
}
```

### 3.7 TaskEvent

```json
{
  "event_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1Z",
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "seq": 4,
  "kind": "task.running",
  "state": "running",
  "source": "adapter",
  "message": "Task accepted by runtime",
  "data": {
    "remote_execution_id": "session_123"
  },
  "created_at": "2026-04-11T10:00:03Z"
}
```

Rules:

- `seq` is strictly increasing per task starting at `1`
- `state` is the task state after applying the event
- `data` must always be JSON-serializable
- event rows are append-only

### 3.8 ArtifactRef

```json
{
  "artifact_id": "01JY0R4R2V2QKBR8Q2YQ9BMS20",
  "media_type": "application/json",
  "url": "http://127.0.0.1:7777/artifacts/01JY0R4R2V2QKBR8Q2YQ9BMS20",
  "size_bytes": 1824,
  "sha256": "hex-string"
}
```

### 3.9 AgentRegistrationRequest

```json
{
  "agent_id": "01JY0R4R2V2QKBR8Q2YQ9BMS20",
  "display_name": "hermes-dev",
  "runtime_kind": "hermes",
  "transport_kind": "local_managed",
  "endpoint": "",
  "runtime_id": "core",
  "capabilities": ["agent.runtime", "code.patch"],
  "sticky_mode": "conversation",
  "metadata": {
    "profile": "aethrolink-agent"
  },
  "lease_ttl_seconds": 300
}
```

Rules:

- `display_name` is required
- `runtime_kind` is required
- `transport_kind` is required
- `agent_id` is optional and generated if omitted
- `capabilities` defaults to `[]`
- `metadata` defaults to `{}`
- `lease_ttl_seconds` defaults to `300`

### 3.10 AgentRecord

```json
{
  "agent_id": "01JY0R4R2V2QKBR8Q2YQ9BMS20",
  "display_name": "hermes-dev",
  "runtime_kind": "hermes",
  "transport_kind": "local_managed",
  "endpoint": "",
  "runtime_id": "core",
  "capabilities": ["agent.runtime", "code.patch"],
  "sticky_mode": "conversation",
  "metadata": {
    "profile": "aethrolink-agent"
  },
  "status": "online",
  "registered_at": "2026-04-14T10:00:00Z",
  "updated_at": "2026-04-14T10:00:00Z",
  "last_seen_at": "2026-04-14T10:00:00Z",
  "lease_expires_at": "2026-04-14T10:05:00Z"
}
```

Rules:

- `status` is `online` or `offline`
- effective online/offline state is determined by lease expiry
- registration updates the agent row in place by `agent_id`
- heartbeat extends `lease_expires_at`

## 4. Agent Control Plane

Registration endpoints:

- `POST /v1/agents/register`
- `POST /v1/agents/{agent_id}/heartbeat`
- `GET /v1/agents`
- `GET /v1/agents/{agent_id}`
- `POST /v1/agents/{agent_id}/unregister`

Invocation remains on the existing task endpoints:

- `POST /v1/tasks`
- `GET /v1/tasks/{task_id}`
- `GET /v1/tasks/{task_id}/events`
- `POST /v1/tasks/{task_id}/resume`
- `POST /v1/tasks/{task_id}/cancel`

### 3.9 NetworkEnvelope

This type is internal in v0.1 but must exist now for future transport support. Multinode task relay should prefer the typed node protocol payloads below instead of interpreting arbitrary `body` maps in core routing.

```json
{
  "envelope_id": "01JY0R4R2V2QKBR8Q2YQ9BMS21",
  "conversation_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1W",
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "sender_node": "local-node",
  "recipient_node": "local-node",
  "message_type": "task.submit",
  "body": {
    "task": {}
  },
  "created_at": "2026-04-11T10:00:00Z",
  "signature": null
}
```

### 3.10 Node Protocol Payloads

`internal/nodeproto` defines the first static-peer HTTP multinode contract. These payloads keep remote transport separate from runtime adapters and make origin-vs-destination ownership explicit.

#### Message types

```text
task.submit
task.accepted
task.event
task.resume
task.cancel
error
```

#### task.submit

```json
{
  "origin_node_id": "node-a",
  "origin_proxy_task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "origin_thread_id": "thread-origin-1",
  "target_agent_id": "researcher",
  "intent": "research.summary",
  "payload": { "topic": "multinode" },
  "runtime_options": { "executor": "research" },
  "trace": { "trace_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1X" },
  "delivery": { "mode": "stream", "launch_if_down": true, "timeout_ms": 60000 },
  "submitted_at": "2026-04-24T07:00:00Z"
}
```

Rules:

- origin node owns `origin_proxy_task_id`
- destination node owns the real execution task it creates after acceptance
- `target_agent_id` is resolved on the destination node
- `intent` and `payload` are required

#### task.accepted

```json
{
  "origin_proxy_task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "destination_node_id": "node-b",
  "destination_task_id": "remote-task-9",
  "destination_thread_id": "thread-remote-2",
  "accepted_at": "2026-04-24T07:00:01Z"
}
```

Rules:

- origin persists this as the remote task binding
- destination task/thread ids are opaque to the origin

#### task.event

```json
{
  "origin_proxy_task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "destination_node_id": "node-b",
  "destination_task_id": "remote-task-9",
  "destination_thread_id": "thread-remote-2",
  "seq": 3,
  "kind": "task.completed",
  "state": "completed",
  "source": "runtime",
  "message": "remote task completed",
  "data": {},
  "remote_execution_id": "run-9",
  "remote_session_id": "session-4",
  "occurred_at": "2026-04-24T07:01:00Z"
}
```

Rules:

- origin stores remote events against `origin_proxy_task_id`
- persisted local event source should be `transport`
- destination ownership metadata must remain inspectable in local event data

#### task.resume and task.cancel

Both control messages must include:

- `origin_node_id`
- `origin_proxy_task_id`
- `destination_task_id`
- `trace`
- `requested_at`

`task.resume` additionally includes `payload`; `task.cancel` additionally includes optional `reason`.

## 4. HTTP API Contracts

## 4.1 POST /v1/tasks

Create a new task.

### Request

Body must follow `TaskCreateRequest`.

### Response

Status: `202 Accepted`

```json
{
  "task": {
    "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
    "conversation_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1W",
    "sender": "local",
    "intent": "code.patch",
    "requested_runtime": "core",
    "resolved_runtime": "core",
    "runtime_options": {
      "executor": "coder"
    },
    "status": "created",
    "remote": null,
    "last_error": null,
    "result_artifact_id": null,
    "created_at": "2026-04-11T10:00:00Z",
    "updated_at": "2026-04-11T10:00:00Z"
  },
  "links": {
    "self": "/v1/tasks/01JY0R4R2V2QKBR8Q2YQ9BMS1V",
    "events": "/v1/tasks/01JY0R4R2V2QKBR8Q2YQ9BMS1V/events"
  }
}
```

### Errors

- `400` invalid JSON or missing required fields
- `404` explicit `target_runtime` not found
- `409` routing conflict or ambiguous route
- `422` target cannot satisfy requested intent
- `500` unexpected internal error

## 4.2 GET /v1/tasks/{task_id}

Fetch a task record.

### Response

Status: `200 OK`

```json
{
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "conversation_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1W",
  "sender": "local",
  "intent": "code.patch",
  "requested_runtime": "core",
  "resolved_runtime": "core",
  "runtime_options": {
    "executor": "coder"
  },
  "status": "running",
  "remote": {
    "binding": "acp_client_stdio",
    "remote_execution_id": "session_123",
    "remote_session_id": "session_123"
  },
  "last_error": null,
  "result_artifact_id": null,
  "created_at": "2026-04-11T10:00:00Z",
  "updated_at": "2026-04-11T10:00:03Z"
}
```

Error: `404` if task does not exist

## 4.3 GET /v1/tasks/{task_id}/events

Server-Sent Events stream of ordered task events.

### SSE format

```text
event: task.event
id: 4
data: {"event_id":"...","task_id":"...","seq":4,"kind":"task.running","state":"running","source":"adapter","message":"Task accepted by runtime","data":{"remote_execution_id":"session_123"},"created_at":"2026-04-11T10:00:03Z"}
```

Rules:

- `id` must equal the event `seq`
- events must be emitted in ascending `seq` order
- keepalive comments may be sent periodically
- if the task is already terminal, the endpoint must stream historical events then close
- if the task is active, the endpoint must stream historical events then continue live

Error: `404` if task does not exist

## 4.4 POST /v1/tasks/{task_id}/resume

Resume a task currently in `awaiting_input`.

### Request

```json
{
  "payload": {
    "approved": true,
    "comment": "Proceed"
  },
  "metadata": {
    "resumed_by": "operator"
  }
}
```

### Response

Status: `202 Accepted`

```json
{
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "status": "running"
}
```

Errors:

- `404` task not found
- `409` task is not in `awaiting_input`
- `422` payload rejected by adapter
- `500` unexpected failure

## 4.5 POST /v1/tasks/{task_id}/cancel

Request cancellation.

### Request

```json
{
  "reason": "Operator requested stop"
}
```

Body is optional.

### Response

Status: `202 Accepted` if cancellation was newly requested  
Status: `200 OK` if the task is already terminal or already cancelled

```json
{
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "status": "running"
}
```

Rules:

- for pre-dispatch tasks, cancellation is immediate
- for active remote tasks, cancellation is best-effort and asynchronous
- cancelling an already completed or failed task is a no-op

## 4.6 GET /v1/runtimes

List runtimes from registry.

### Response

Status: `200 OK`

```json
{
  "runtimes": [
    {
      "runtime_id": "hermes",
      "adapter": "hermes",
      "capabilities": ["code.patch", "code.review", "research.topic"],
      "defaults": {
        "executor": "coder"
      }
    }
  ]
}
```

## 4.7 GET /v1/runtimes/{runtime_id}/health

Health of runtime using default options only.

### Response

Status: `200 OK`

```json
{
  "runtime_id": "hermes",
  "healthy": true,
  "details": {
    "adapter": "hermes",
    "worker_count": 1
  }
}
```

## 4.8 POST /v1/runtimes/{runtime_id}/start

Start or warm a runtime.

### Request

```json
{
  "runtime_options": {
    "executor": "coder"
  }
}
```

### Response

Status: `202 Accepted`

```json
{
  "runtime_id": "hermes",
  "started": true,
  "lease_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1Y"
}
```

## 4.9 POST /v1/runtimes/{runtime_id}/stop

Stop or release a runtime context.

### Request

```json
{
  "runtime_options": {
    "executor": "coder"
  }
}
```

### Response

Status: `202 Accepted`

```json
{
  "runtime_id": "hermes",
  "stopped": true
}
```

## 5. Persistence Contracts

Use SQLite.

## 5.1 Table: runtimes

Required columns:

- `runtime_id TEXT PRIMARY KEY`
- `adapter_kind TEXT NOT NULL`
- `spec_json TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

## 5.2 Table: runtime_leases

Required columns:

- `lease_id TEXT PRIMARY KEY`
- `runtime_id TEXT NOT NULL`
- `subcontext_key TEXT`
- `process_id TEXT`
- `metadata_json TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `released_at TEXT`

Index:

- `(runtime_id, subcontext_key, released_at)`

## 5.3 Table: tasks

Required columns:

- `task_id TEXT PRIMARY KEY`
- `conversation_id TEXT NOT NULL`
- `sender TEXT NOT NULL`
- `intent TEXT NOT NULL`
- `requested_runtime TEXT`
- `resolved_runtime TEXT`
- `runtime_options_json TEXT NOT NULL`
- `payload_artifact_id TEXT`
- `status TEXT NOT NULL`
- `remote_binding TEXT`
- `remote_execution_id TEXT`
- `remote_session_id TEXT`
- `last_error_json TEXT`
- `result_artifact_id TEXT`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

Indexes:

- `(conversation_id)`
- `(status)`
- `(resolved_runtime, status)`

## 5.4 Table: task_events

Required columns:

- `event_id TEXT PRIMARY KEY`
- `task_id TEXT NOT NULL`
- `seq INTEGER NOT NULL`
- `kind TEXT NOT NULL`
- `state TEXT NOT NULL`
- `source TEXT NOT NULL`
- `message TEXT`
- `data_json TEXT NOT NULL`
- `created_at TEXT NOT NULL`

Constraints:

- unique `(task_id, seq)`

Index:

- `(task_id, seq)`

## 5.5 Table: artifacts

Required columns:

- `artifact_id TEXT PRIMARY KEY`
- `media_type TEXT NOT NULL`
- `relative_path TEXT NOT NULL`
- `size_bytes INTEGER NOT NULL`
- `sha256 TEXT NOT NULL`
- `created_at TEXT NOT NULL`

## 5.6 Table: launch_history

Required columns:

- `launch_id TEXT PRIMARY KEY`
- `runtime_id TEXT NOT NULL`
- `subcontext_key TEXT`
- `command_json TEXT NOT NULL`
- `pid TEXT`
- `status TEXT NOT NULL`
- `error_text TEXT`
- `started_at TEXT NOT NULL`
- `ended_at TEXT`

## 6. Artifact Contract

Artifacts larger than 16 KB should be stored out-of-row by default.

Rules:

- store artifact bytes on disk
- store only metadata in SQLite
- serve artifacts over local HTTP
- use `application/json` for JSON payload artifacts
- use `text/plain` for log output artifacts
- use `text/markdown` for agent text output when appropriate

## 7. ACP Communication Adapter Payload Contract

When the HTTP ACP runtime adapter submits a task, it must prepend an AethroLink control part.

### First message part

- `content_type`: `application/vnd.aethrolink.control+json`
- `content`: UTF-8 JSON string

Example control object:

```json
{
  "protocol": "aethrolink/0.1-local",
  "task_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1V",
  "conversation_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1W",
  "sender": "local",
  "intent": "research.topic",
  "delivery": {
    "mode": "stream",
    "launch_if_down": true,
    "timeout_ms": 60000
  },
  "trace": {
    "trace_id": "01JY0R4R2V2QKBR8Q2YQ9BMS1X",
    "parent_task_id": null
  },
  "metadata": {
    "requested_by": "planner"
  }
}
```

### Second message part

Use one of:

- `application/json` if payload is JSON
- `text/plain` if payload is plain text
- `content_url` for externally stored artifacts when needed

## 8. Contract Shortcuts

To reduce confusion during implementation:

- do not invent extra task states
- do not invent extra top-level runtime identities
- do not store large payloads inline in `task_events`
- do not use remote protocol IDs as primary local IDs
- do not collapse `RuntimeLease` and `RemoteHandle` into one type
