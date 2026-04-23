package adapters

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/adaptersupport"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type acpRun struct {
	events chan atypes.TaskEvent
	errs   chan error
}

// ACPAdapter is the orchestrator-facing adapter facade. Internally it composes
// a local runtime host, ACP session transport, sticky-session store, and
// runtime-specific dialects.
type ACPAdapter struct {
	discovery atypes.DiscoveryProvider
	host      atypes.LocalRuntimeHost
	transport atypes.LocalSessionTransport
	runtime   *runtime.Manager
	sessions  *adaptersupport.SessionCoordinator
	dialects  map[string]acpDialect
	mu        sync.Mutex
	runs      map[string]*acpRun
}

func NewACPAdapter(discovery atypes.DiscoveryProvider, runtimeManager *runtime.Manager) *ACPAdapter {
	return &ACPAdapter{
		discovery: discovery,
		host:      newACPRuntimeHost(runtimeManager),
		transport: newACPSessionTransport(runtimeManager),
		runtime:   runtimeManager,
		sessions:  adaptersupport.NewSessionCoordinator(runtimeManager.Store()),
		dialects:  defaultACPDialects(),
		runs:      map[string]*acpRun{},
	}
}

func (a *ACPAdapter) AdapterName() string { return "acp" }

func (a *ACPAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "acp", "mode": "acp_bridge"}, nil
}

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
	return a.host.EnsureRunning(ctx, scopedSpec, dialect.SubcontextKey(resolvedOptions))
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

	binding := dialect.WorkspaceBinding(task)
	if binding.CWD == "" {
		binding.CWD = "."
	}
	idleTimeout := adaptersupport.SessionIdleTimeout(task.RuntimeOptions)
	promptTimeout := idleTimeout
	if promptTimeout < time.Minute {
		promptTimeout = time.Minute
	}
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

func (a *ACPAdapter) consumeRun(ctx context.Context, run *acpRun, sub <-chan map[string]any, cancel func(), task atypes.TaskEnvelope, lease atypes.RuntimeLease, dialect acpDialect, binding atypes.WorkspaceBinding, stickyKey, sessionID, promptText string, promptTimeout, idleTimeout time.Duration) {
	defer a.deleteRun(task.TaskID)
	defer cancel()
	defer close(run.events)
	defer close(run.errs)

	scope := adaptersupport.SessionScope(task.TargetAgentID, lease.SubcontextKey, stickyKey)
	trackerState := &acpRunTracker{}
	tracker := &acpNotificationTracker{run: trackerState}
	lastActivity := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	promptResult := make(chan error, 1)
	promptFinished := false
	drainUntil := time.Time{}

	run.events <- runtimeEventToTaskEvent(task.TaskID, dialect.AcceptedEvent(task, sessionID))
	go func() {
		promptResult <- a.transport.Prompt(
			ctx,
			task.TargetAgentID,
			lease.SubcontextKey,
			sessionID,
			dialect.PromptPayload(sessionID, promptText, binding, task.RuntimeOptions),
			promptTimeout,
		)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-promptResult:
			if err != nil {
				a.sessions.Release(scope, task.TaskID)
				run.errs <- err
				return
			}
			promptFinished = true
			drainUntil = time.Now().Add(500 * time.Millisecond)
		case <-ticker.C:
			if promptFinished && !drainUntil.IsZero() && time.Now().After(drainUntil) {
				finalText := adapterTrimText(trackerState.FinalText())
				if dialect.ShouldRecoverFinalText(finalText, trackerState.sawStructuredEvents) {
					finalText = a.recoverFinalText(context.Background(), task.TargetAgentID, lease.SubcontextKey, sessionID, binding, task.RuntimeOptions, dialect)
				}
				trackerState.SetFinalText(finalText)
				if syntheticCompletionDecision(dialect, trackerState) {
					run.events <- runtimeEventToTaskEvent(task.TaskID, dialect.CompletionEvent(task, finalText))
				}
				a.sessions.Release(scope, task.TaskID)
				return
			}
			if idleTimeout > 0 && time.Since(lastActivity) > idleTimeout {
				a.sessions.Release(scope, task.TaskID)
				run.errs <- fmt.Errorf("%s session idle timeout exceeded", dialect.Name())
				return
			}
		case msg, ok := <-sub:
			if !ok {
				return
			}
			params, _ := msg["params"].(map[string]any)
			if params == nil || !sessionMatches(params, sessionID) {
				continue
			}
			lastActivity = time.Now()
			if promptFinished {
				drainUntil = time.Now().Add(250 * time.Millisecond)
			}
			_ = a.sessions.TouchActivity(context.Background(), task.TargetAgentID, lease.SubcontextKey, stickyKey, lastActivity.UTC())
			events, terminal := dialect.HandleNotification(params, tracker)
			trackerState.Record(events)
			for _, event := range events {
				run.events <- runtimeEventToTaskEvent(task.TaskID, event)
			}
			if terminal {
				a.sessions.Release(scope, task.TaskID)
				return
			}
		}
	}
}

func (a *ACPAdapter) deleteRun(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, taskID)
}

func (a *ACPAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	a.mu.Lock()
	run := a.runs[handle.TaskID]
	a.mu.Unlock()
	if run == nil {
		events := make(chan atypes.TaskEvent)
		errs := make(chan error, 1)
		close(events)
		errs <- fmt.Errorf("missing acp run")
		close(errs)
		return events, errs
	}
	out := make(chan atypes.TaskEvent, 64)
	errOut := make(chan error, 4)
	go func() {
		defer close(out)
		defer close(errOut)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-run.events:
				if !ok {
					return
				}
				out <- event
			case err, ok := <-run.errs:
				if ok && err != nil {
					errOut <- err
				}
				return
			}
		}
	}()
	return out, errOut
}

func (a *ACPAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	dialect, err := a.dialectFromHandle(handle)
	if err != nil {
		return err
	}
	resumePayload, err := dialect.ResumePayload(handle.RemoteSessionID, payload)
	if err != nil {
		return err
	}
	return a.transport.Resume(ctx, handle.TargetID, dialect.SubcontextKey(handle.AdapterState), handle.RemoteSessionID, resumePayload)
}

func (a *ACPAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	dialect, err := a.dialectFromHandle(handle)
	if err != nil {
		return err
	}
	return a.transport.Cancel(ctx, handle.TargetID, dialect.SubcontextKey(handle.AdapterState), handle.RemoteSessionID)
}

func (a *ACPAdapter) Health(ctx context.Context, targetID string, options map[string]any) (map[string]any, error) {
	spec, err := a.discovery.ResolveRuntime(ctx, targetID)
	if err != nil {
		return nil, err
	}
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return nil, err
	}
	resolvedOptions := mergeAdapterOptions(spec.Defaults, options)
	return a.host.Health(ctx, spec, dialect.SubcontextKey(resolvedOptions))
}

func (a *ACPAdapter) RehydrateHandle(task atypes.TaskRecord, spec atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	handle := atypes.RemoteHandle{TaskID: task.TaskID, TargetID: task.ResolvedAgentID}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	handle.AdapterState = dialect.RehydrateState(task)
	return handle, nil
}

func (a *ACPAdapter) SubcontextKey(spec atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return ""
	}
	return dialect.SubcontextKey(mergeAdapterOptions(spec.Defaults, runtimeOptions))
}

func (a *ACPAdapter) recoverFinalText(ctx context.Context, targetID, subcontextKey, sessionID string, binding atypes.WorkspaceBinding, runtimeOptions map[string]any, dialect acpDialect) string {
	if sessionID == "" {
		return ""
	}
	return adaptersupport.RecoverAssistantTextFromSessionLoad(
		ctx,
		a.runtime.GetStdioWorker(targetID, subcontextKey),
		sessionID,
		dialect.LoadSessionPayload(sessionID, binding, runtimeOptions),
	)
}
