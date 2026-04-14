# Coding Agent Brief

This file is the entry point for the coding agent.

Do not treat this file as a restatement of the architecture.
The architecture already lives in `overview.md`.

## Read order

Read these files in this exact order:

1. `README.md`
2. `docs/overview.md`
3. `docs/contracts.md`
4. `docs/state_machine.md`
5. `docs/adapter_contracts.md`
6. `docs/implementation_plan.md`

## What each file is for

- `README.md`
  - repository framing
  - non-normative product context

- `docs/overview.md`
  - architectural boundaries
  - package layout
  - major abstractions
  - what belongs where

- `docs/contracts.md`
  - exact HTTP payloads
  - exact event payloads
  - exact database shape
  - exact internal types that must exist

- `docs/state_machine.md`
  - exact task states
  - exact allowed transitions
  - exact cancel/resume/failure behavior

- `docs/adapter_contracts.md`
  - exact adapter semantics
  - exact runtime option handling
  - exact fake runtime behavior for tests

- `docs/implementation_plan.md`
  - build order
  - package-by-package sequence
  - acceptance gates for each phase

## Conflict resolution

If two docs appear to conflict, use this priority order:

1. `docs/contracts.md`
2. `docs/state_machine.md`
3. `docs/adapter_contracts.md`
4. `docs/overview.md`
5. `README.md`

The more specific document wins over the more general document.

## Implementation goal

Build AethroLink Local `v0.1` as a runnable Go module that:

- accepts task submissions
- routes by runtime and intent
- launches runtimes when needed
- supports Hermes through a runtime adapter
- supports OpenClaw through a runtime adapter
- supports HTTP ACP-style runtimes through a runtime adapter
- persists tasks and events in SQLite
- serves artifacts over local HTTP
- streams ordered task events over SSE
- includes fake runtimes for tests
- builds and passes tests

## Non-negotiable constraints

- Go-first implementation for the core and control plane
- no Python services in the core implementation
- no dynamic plugin system in v0.1
- no blockchain features in v0.1
- no libp2p or Waku implementation in v0.1
- keep `RuntimeAdapter`, `TransportAdapter`, `DiscoveryProvider`, and `IdentityProvider` as real interfaces now
- Hermes executors are not public runtime IDs
- OpenClaw session keys are not public runtime IDs
- local `task_id` is always primary
- remote protocol IDs are always secondary

## Decision policy

When something is underspecified:

- prefer the simplest implementation that satisfies the contract docs
- keep APIs explicit and typed
- do not invent new top-level concepts unless required
- do not add speculative features
- keep the repository runnable at every stage

## Deliverable expectation

Produce a complete first implementation draft of `v0.1`, not a partial scaffold.

That means:

- module builds
- server runs
- tests run
- fake runtimes exist
- docs match code
- implementation follows the files above instead of improvising a new architecture
