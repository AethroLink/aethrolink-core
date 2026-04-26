package adapters

import (
	"context"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/adaptersupport"
	runtimemgr "github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func (a *ACPAdapter) EnsureReady(ctx context.Context, targetID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.discovery.ResolveRuntime(ctx, targetID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	resolvedOptions := mergeAdapterOptions(spec.Defaults, options)
	command, err := dialect.Command(spec, resolvedOptions)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	scopedSpec := spec
	scopedSpec.Launch.Command = append([]string(nil), command...)
	subcontextKey := dialect.SubcontextKey(resolvedOptions)
	lease, err := a.host.EnsureRunning(ctx, scopedSpec, subcontextKey)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	// Process liveness is not ACP readiness; probe JSON-RPC before reusing a lease.
	if err := a.transport.Initialize(ctx, targetID, subcontextKey, map[string]any{"protocolVersion": 1}, acpInitializeTimeout(resolvedOptions)); err != nil {
		if !runtimemgr.IsRPCTimeout(err) {
			return atypes.RuntimeLease{}, err
		}
		lease, err = a.host.EnsureRunning(ctx, scopedSpec, subcontextKey)
		if err != nil {
			return atypes.RuntimeLease{}, err
		}
		if err := a.transport.Initialize(ctx, targetID, subcontextKey, map[string]any{"protocolVersion": 1}, acpInitializeTimeout(resolvedOptions)); err != nil {
			return atypes.RuntimeLease{}, err
		}
	}
	return lease, nil
}

func (a *ACPAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	spec, err := a.discovery.ResolveRuntime(ctx, task.TargetAgentID)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	return a.submitPrompt(ctx, task, lease, dialect)
}

// PredictSessionReuse checks whether the next ACP turn can reuse its persisted session.
func (a *ACPAdapter) PredictSessionReuse(ctx context.Context, task atypes.TaskEnvelope) (bool, error) {
	spec, err := a.discovery.ResolveRuntime(ctx, task.TargetAgentID)
	if err != nil {
		return false, err
	}
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return false, err
	}
	resolvedOptions := mergeAdapterOptions(spec.Defaults, task.RuntimeOptions)
	stickyKey := dialect.StickyKey(atypes.TaskEnvelope{
		ThreadID:       task.ThreadID,
		ConversationID: task.ConversationID,
		RuntimeOptions: resolvedOptions,
		TaskID:         task.TaskID,
	})
	subcontextKey := dialect.SubcontextKey(resolvedOptions)
	bound, exists, err := a.sessions.LoadBinding(ctx, task.TargetAgentID, subcontextKey, stickyKey)
	if err != nil || !exists {
		return false, err
	}
	if adaptersupport.SessionBindingStale(bound, adaptersupport.SessionIdleTimeout(resolvedOptions), atypes.NowUTC()) {
		return false, nil
	}
	binding := normalizeACPWorkspaceBinding(dialect.WorkspaceBinding(atypes.TaskEnvelope{RuntimeOptions: resolvedOptions}))
	handshakeTimeout := acpSessionSetupTimeout(resolvedOptions)
	if err := a.transport.Initialize(ctx, task.TargetAgentID, subcontextKey, map[string]any{"protocolVersion": 1}, handshakeTimeout); err != nil {
		return false, nil
	}
	_, err = a.transport.LoadSession(ctx, task.TargetAgentID, subcontextKey, bound.RemoteSessionID, dialect.LoadSessionPayload(bound.RemoteSessionID, binding, resolvedOptions), handshakeTimeout)
	return err == nil, nil
}

func (a *ACPAdapter) submitPrompt(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease, dialect acpDialect) (atypes.RemoteHandle, error) {
	stickyKey := dialect.StickyKey(task)
	scope := adaptersupport.SessionScope(task.TargetAgentID, lease.SubcontextKey, stickyKey)
	if err := a.sessions.TryAcquire(scope, task.TaskID); err != nil {
		return atypes.RemoteHandle{}, err
	}
	if err := a.transport.Initialize(ctx, task.TargetAgentID, lease.SubcontextKey, map[string]any{"protocolVersion": 1}, acpInitializeTimeout(task.RuntimeOptions)); err != nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, err
	}

	binding := normalizeACPWorkspaceBinding(dialect.WorkspaceBinding(task))
	idleTimeout := adaptersupport.SessionIdleTimeout(task.RuntimeOptions)
	promptTimeout := acpPromptTimeout(idleTimeout)
	now := atypes.NowUTC()
	sessionID, err := a.loadOrCreateSession(ctx, task, lease, dialect, binding, stickyKey, now)
	if err != nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, err
	}
	if err := a.sessions.PersistBinding(ctx, task.TargetAgentID, lease.SubcontextKey, stickyKey, dialect.BindingName(), sessionID, dialect.BindingMetadata(task.RuntimeOptions, binding), now); err != nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, err
	}

	run := &acpRun{events: make(chan atypes.TaskEvent, 64), errs: make(chan error, 4)}
	a.mu.Lock()
	a.runs[task.TaskID] = run
	a.mu.Unlock()

	sub, cancel, err := a.transport.Stream(ctx, task.TargetAgentID, lease.SubcontextKey)
	if err != nil {
		a.sessions.Release(scope, task.TaskID)
		a.deleteRun(task.TaskID)
		return atypes.RemoteHandle{}, err
	}
	go a.consumeRun(ctx, run, sub, cancel, task, lease, dialect, binding, stickyKey, sessionID, dialect.PromptText(task), promptTimeout, idleTimeout)

	return atypes.RemoteHandle{
		TaskID:            task.TaskID,
		TargetID:          task.TargetAgentID,
		Binding:           dialect.BindingName(),
		RemoteExecutionID: sessionID,
		RemoteSessionID:   sessionID,
		AdapterState:      dialect.AdapterState(task, sessionID),
	}, nil
}

func (a *ACPAdapter) loadOrCreateSession(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease, dialect acpDialect, binding atypes.WorkspaceBinding, stickyKey string, now time.Time) (string, error) {
	bound, exists, err := a.sessions.LoadBinding(ctx, task.TargetAgentID, lease.SubcontextKey, stickyKey)
	if err != nil {
		return "", err
	}
	idleTimeout := adaptersupport.SessionIdleTimeout(task.RuntimeOptions)
	handshakeTimeout := acpSessionSetupTimeout(task.RuntimeOptions)
	if exists && !adaptersupport.SessionBindingStale(bound, idleTimeout, now) {
		sessionID, err := a.transport.LoadSession(
			ctx,
			task.TargetAgentID,
			lease.SubcontextKey,
			bound.RemoteSessionID,
			dialect.LoadSessionPayload(bound.RemoteSessionID, binding, task.RuntimeOptions),
			handshakeTimeout,
		)
		if err == nil {
			return sessionID, nil
		}
	}
	return a.transport.OpenSession(ctx, task.TargetAgentID, lease.SubcontextKey, dialect.OpenSessionPayload(binding, task.RuntimeOptions), handshakeTimeout)
}
