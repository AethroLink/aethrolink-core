# State Machine

This document is **normative** for task state transitions and task lifecycle behavior.

It intentionally avoids restating API shape or architecture.

## 1. Canonical Task States

These are the only valid task states in v0.1:

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

No other persistent task states may be added in v0.1.

## 2. General Rules

1. A task starts in `created`.
2. A task ends in exactly one terminal state:
   - `completed`
   - `failed`
   - `cancelled`
3. Task state changes are monotonic and append-only in the event log.
4. Every state transition must emit exactly one `TaskEvent`.
5. `task_id` remains stable even if no remote execution exists yet.
6. Remote identifiers may remain null for the entire lifetime of a failed pre-dispatch task.
7. Duplicate adapter events must not create duplicate state transitions.
8. `seq` must increase by 1 for every persisted event of a task.

## 3. Transition Table

| From | Trigger | To | Required side effects |
|---|---|---|---|
| `created` | runtime already healthy and lease acquired | `ready` | persist lease if any |
| `created` | runtime unavailable and `launch_if_down=true` | `pending_launch` | persist routing decision |
| `created` | runtime unavailable and `launch_if_down=false` | `failed` | set last error = `runtime_unavailable` |
| `created` | routing failure | `failed` | set last error = `route_not_found` or `route_ambiguous` |
| `pending_launch` | supervisor accepted launch | `launching` | create launch history row |
| `pending_launch` | cancel requested | `cancelled` | mark task terminal immediately |
| `launching` | runtime became healthy and lease acquired | `ready` | persist lease metadata |
| `launching` | launch failed or timed out | `failed` | set last error = `launch_failed` |
| `launching` | cancel requested | `cancelled` | stop launch if possible |
| `ready` | orchestrator starts submit | `dispatching` | call adapter submit |
| `ready` | cancel requested before submit starts | `cancelled` | no remote cancellation needed |
| `dispatching` | adapter returned remote handle | `running` | persist remote handle |
| `dispatching` | adapter submit failed | `failed` | set last error = `submit_failed` |
| `dispatching` | cancel requested before remote handle exists | `cancelled` | no remote cancellation needed |
| `running` | adapter emitted await signal | `awaiting_input` | persist await payload if any |
| `running` | adapter emitted completion | `completed` | persist result artifact if any |
| `running` | adapter emitted failure | `failed` | set last error = `remote_failed` |
| `running` | local timeout without successful cancel | `failed` | set last error = `timeout` |
| `awaiting_input` | resume accepted | `running` | persist resume event |
| `awaiting_input` | adapter emitted completion without resume | `completed` | persist result artifact if any |
| `awaiting_input` | adapter emitted failure | `failed` | set last error = `remote_failed` |
| `awaiting_input` | local timeout without successful cancel | `failed` | set last error = `timeout` |

## 4. State Semantics

### 4.1 `created`

The task exists locally but no runtime readiness check or launch outcome has been finalized yet.

Required conditions:

- task row exists
- event `task.created` exists
- no remote handle exists

### 4.2 `pending_launch`

The target runtime is not yet available and launch is required before submit.

Required conditions:

- `launch_if_down=true`
- no ready lease yet
- no remote handle yet

### 4.3 `launching`

A launch command has been issued and AethroLink is waiting for readiness.

Required conditions:

- launch history row exists
- no remote handle yet

### 4.4 `ready`

A runtime lease exists and the runtime is considered usable for submit.

Required conditions:

- a valid `RuntimeLease` exists
- no remote handle may exist yet

### 4.5 `dispatching`

The adapter is actively submitting the task to the runtime.

Required conditions:

- runtime lease may or may not exist depending on adapter type
- remote handle is not yet persisted

### 4.6 `running`

The runtime has accepted the work.

Required conditions:

- remote handle exists
- task may continue to emit progress events

### 4.7 `awaiting_input`

The runtime is paused waiting for external input.

Required conditions:

- remote handle exists
- the task is resumable
- a resume payload may be supplied via the API

### 4.8 Terminal states

#### `completed`
The task ended successfully.

#### `failed`
The task ended unsuccessfully and will not be retried automatically.

#### `cancelled`
The task was cancelled intentionally and is terminal.

## 5. Cancel Behavior

Cancellation is **idempotent**.

## 5.1 Cancel before remote acceptance

If the task is in one of these states:

- `created`
- `pending_launch`
- `launching`
- `ready`
- `dispatching`

then cancellation is immediate:

- emit `task.cancel_requested`
- emit `task.cancelled`
- final state becomes `cancelled`

No remote cancellation call is required if no remote handle exists.

## 5.2 Cancel after remote acceptance

If the task is in one of these states:

- `running`
- `awaiting_input`

then cancellation is asynchronous:

1. emit `task.cancel_requested`
2. invoke adapter `cancel(handle)`
3. remain in current state until adapter confirms cancellation or failure
4. on successful adapter cancellation, emit `task.cancelled` and enter `cancelled`
5. on cancellation failure, enter `failed` with `last_error.reason = "cancel_failed"`

## 5.3 Cancel on terminal task

If the task is already terminal, cancel is a no-op.

## 6. Resume Behavior

Resume is only valid from `awaiting_input`.

Rules:

- `POST /resume` on any other state returns `409`
- a successful resume request must emit `task.resume_requested`
- if the adapter accepts resume immediately, emit `task.resumed` and move to `running`
- if adapter resume fails, remain in `awaiting_input` and return `422` or `500` to caller depending on failure class

## 7. Failure Mapping

Use these normalized failure reasons in `last_error.reason`:

- `route_not_found`
- `route_ambiguous`
- `runtime_unavailable`
- `launch_failed`
- `submit_failed`
- `protocol_error`
- `remote_failed`
- `resume_failed`
- `cancel_failed`
- `timeout`
- `internal_error`

Adapters may include extra detail in `last_error.detail`, but `reason` must be one of the above.

## 8. Timeout Rules

Timeouts are best-effort in v0.1.

Rules:

- if `delivery.timeout_ms` is set, the orchestrator should apply it from the moment the task enters `dispatching`
- if timeout fires before completion:
  - emit `task.cancel_requested`
  - attempt adapter cancellation if remote handle exists
  - if cancellation succeeds, final state becomes `cancelled`
  - otherwise final state becomes `failed` with reason `timeout`

## 9. Retry Rules

Automatic retries are intentionally minimal in v0.1.

Rules:

- do not retry route resolution
- do not retry submit after a submit failure
- do not retry resume automatically
- do not retry remote task execution automatically
- a supervisor may make one launch attempt per task by default
- if the spawned process exits before readiness, the task fails unless an explicit adapter-specific restart policy says otherwise

## 10. Event Emission Rules

Every state change must emit a corresponding event.

Recommended mapping:

| State change | Event kind |
|---|---|
| task created | `task.created` |
| route resolved | `task.routed` |
| launch needed | `runtime.pending_launch` |
| launch started | `runtime.launching` |
| runtime ready | `runtime.ready` |
| submit started | `task.dispatching` |
| remote accepted | `task.running` |
| runtime paused | `task.awaiting_input` |
| resume requested | `task.resume_requested` |
| resume accepted | `task.resumed` |
| cancel requested | `task.cancel_requested` |
| success | `task.completed` |
| failure | `task.failed` |
| cancellation confirmed | `task.cancelled` |

## 11. Deduplication Rules

The core is responsible for deduplication.

Rules:

- if an adapter repeats a terminal event, ignore duplicates after terminal state is recorded
- if an adapter repeats a progress event with the same adapter event identity, ignore it
- deduplication keys may be adapter-specific and stored in adapter state
- deduplication must never reorder events

## 12. Recovery After Restart

On process restart:

- historical task state is reconstructed from `tasks` plus ordered `task_events`
- tasks already in terminal states remain terminal
- tasks in non-terminal states are restored as persisted
- v0.1 does not guarantee automatic continuation of in-flight external work after process restart
- the system must still preserve history and surface correct status to API clients

## 13. Summary Rules

The coding agent should not improvise lifecycle behavior outside this file.

Especially:

- do not add extra persistent states
- do not auto-retry remote execution
- do not skip event creation for state changes
- do not make cancel non-idempotent
